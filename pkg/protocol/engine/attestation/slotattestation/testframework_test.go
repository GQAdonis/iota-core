package slotattestation_test

import (
	"crypto/ed25519"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ads"
	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/core/account"
	"github.com/iotaledger/iota-core/pkg/core/api"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/attestation/slotattestation"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/builder"
	"github.com/iotaledger/iota.go/v4/tpkg"
)

type issuer struct {
	accountID iotago.AccountID
	priv      ed25519.PrivateKey
}

type TestFramework struct {
	test     *testing.T
	Instance *slotattestation.Manager

	bucketedStorage *shrinkingmap.ShrinkingMap[iotago.SlotIndex, kvstore.KVStore]

	attestationsByAlias *shrinkingmap.ShrinkingMap[string, *iotago.Attestation]
	issuerByAlias       *shrinkingmap.ShrinkingMap[string, *issuer]

	uniqueCounter atomic.Int64
	mutex         sync.RWMutex
}

func NewTestFramework(test *testing.T) *TestFramework {
	t := &TestFramework{
		test:                test,
		bucketedStorage:     shrinkingmap.New[iotago.SlotIndex, kvstore.KVStore](),
		attestationsByAlias: shrinkingmap.New[string, *iotago.Attestation](),
		issuerByAlias:       shrinkingmap.New[string, *issuer](),
	}

	bucketedStorage := func(index iotago.SlotIndex) kvstore.KVStore {
		return lo.Return1(t.bucketedStorage.GetOrCreate(index, func() kvstore.KVStore {
			return mapdb.NewMapDB()
		}))
	}

	committeeFunc := func(index iotago.SlotIndex) *account.SeatedAccounts {
		accounts := account.NewAccounts()
		var members []iotago.AccountID
		t.issuerByAlias.ForEach(func(alias string, issuer *issuer) bool {
			accounts.Set(issuer.accountID, &account.Pool{}) // we don't care about pools with PoA
			members = append(members, issuer.accountID)
			return true
		})
		return accounts.SelectCommittee(members...)
	}

	t.Instance = slotattestation.NewManager(
		0,
		0,
		2,
		bucketedStorage,
		committeeFunc,
		api.NewStaticProvider(tpkg.TestAPI),
	)

	return t
}

func (t *TestFramework) issuer(alias string) *issuer {
	return lo.Return1(t.issuerByAlias.GetOrCreate(alias, func() *issuer {
		pub, priv, err := ed25519.GenerateKey(nil)
		require.NoError(t.test, err)

		accountID := iotago.AccountID(*iotago.Ed25519AddressFromPubKey(pub))
		accountID.RegisterAlias(alias)

		return &issuer{
			accountID: accountID,
			priv:      priv,
		}
	}))
}

func (t *TestFramework) AddFutureAttestation(issuerAlias string, attestationAlias string, blockSlot iotago.SlotIndex, attestedSlot iotago.SlotIndex) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	issuer := t.issuer(issuerAlias)
	issuingTime := tpkg.TestAPI.TimeProvider().SlotStartTime(blockSlot).Add(time.Duration(t.uniqueCounter.Add(1)))

	block, err := builder.NewBasicBlockBuilder(tpkg.TestAPI).
		IssuingTime(issuingTime).
		SlotCommitmentID(iotago.NewCommitment(tpkg.TestAPI.Version(), attestedSlot, iotago.CommitmentID{}, iotago.Identifier{}, 0).MustID()).
		Sign(issuer.accountID, issuer.priv).
		Build()
	require.NoError(t.test, err)

	block.MustID(tpkg.TestAPI).RegisterAlias(attestationAlias)
	att := iotago.NewAttestation(tpkg.TestAPI, block)
	t.attestationsByAlias.Set(attestationAlias, att)

	modelBlock, err := model.BlockFromBlock(block, tpkg.TestAPI)
	require.NoError(t.test, err)

	t.Instance.AddAttestationFromBlock(blocks.NewBlock(modelBlock))
}

func (t *TestFramework) blockIDFromAttestation(att *iotago.Attestation) iotago.BlockID {
	return lo.PanicOnErr(att.BlockID(tpkg.TestAPI))
}

func (t *TestFramework) attestation(alias string) *iotago.Attestation {
	attestation, exists := t.attestationsByAlias.Get(alias)
	require.Truef(t.test, exists, "attestation with alias '%s' does not exist", alias)

	return attestation
}

func (t *TestFramework) AssertCommit(slot iotago.SlotIndex, expectedCW uint64, expectedAttestationsAliases map[string]string, optExpectedGetError ...bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	cw, root, err := t.Instance.Commit(slot)
	require.NoError(t.test, err)

	require.EqualValues(t.test, expectedCW, cw)

	expectedTree := *ads.NewMap[iotago.AccountID, *iotago.Attestation](mapdb.NewMapDB(),
		iotago.Identifier.Bytes,
		iotago.IdentifierFromBytes,
		func(attestation *iotago.Attestation) ([]byte, error) {
			return tpkg.TestAPI.Encode(attestation)
		},
		func(bytes []byte) (*iotago.Attestation, int, error) {
			att := new(iotago.Attestation)
			n, err := tpkg.TestAPI.Decode(bytes, att)

			return att, n, err
		},
	)
	expectedAttestations := make([]*iotago.Attestation, 0)
	for issuerAlias, attestationAlias := range expectedAttestationsAliases {
		expectedTree.Set(t.issuer(issuerAlias).accountID, t.attestation(attestationAlias))
		expectedAttestations = append(expectedAttestations, t.attestation(attestationAlias))
	}

	// Retrieve attestations from storage and compare them with the expected ones.
	tree, err := t.Instance.GetMap(slot)

	attestationFromTree := make([]*iotago.Attestation, 0)
	attestationBlockIDsFromTree := make([]iotago.BlockID, 0)
	if len(optExpectedGetError) == 1 && optExpectedGetError[0] {
		require.ErrorContains(t.test, err, "is smaller than attestation")
		return
	} else {
		require.NoError(t.test, err)
	}

	require.NoError(t.test, tree.Stream(func(key iotago.AccountID, value *iotago.Attestation) bool {
		attestationFromTree = append(attestationFromTree, value)
		attestationBlockIDsFromTree = append(attestationBlockIDsFromTree, t.blockIDFromAttestation(value))
		return true
	}))

	require.ElementsMatchf(t.test, expectedAttestations, attestationFromTree, "attestations from tree do not match expected ones: expected: %v, got: %v", lo.Values(expectedAttestationsAliases), attestationBlockIDsFromTree)

	require.Equal(t.test, expectedTree.Root(), root)
}