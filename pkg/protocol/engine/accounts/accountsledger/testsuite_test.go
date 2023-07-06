package accountsledger_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/core/memstorage"
	"github.com/iotaledger/hive.go/crypto/ed25519"
	"github.com/iotaledger/hive.go/ds/advancedset"
	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/accounts"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/accounts/accountsledger"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	"github.com/iotaledger/iota-core/pkg/storage/prunable"
	"github.com/iotaledger/iota-core/pkg/utils"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/tpkg"
)

type TestSuite struct {
	T *testing.T

	accounts map[string]iotago.AccountID
	pubKeys  map[string]ed25519.PublicKey
	outputs  map[string]iotago.OutputID

	slotData               *shrinkingmap.ShrinkingMap[iotago.SlotIndex, *slotData]
	accountsStatePerSlot   *shrinkingmap.ShrinkingMap[iotago.SlotIndex, map[iotago.AccountID]*AccountState]
	latestFieldsPerAccount *shrinkingmap.ShrinkingMap[iotago.AccountID, *latestAccountFields]
	blocks                 *memstorage.IndexedStorage[iotago.SlotIndex, iotago.BlockID, *blocks.Block]
	Instance               *accountsledger.Manager
}

func NewTestSuite(test *testing.T) *TestSuite {
	t := &TestSuite{
		T:        test,
		accounts: make(map[string]iotago.AccountID),
		pubKeys:  make(map[string]ed25519.PublicKey),
		outputs:  make(map[string]iotago.OutputID),

		blocks:                 memstorage.NewIndexedStorage[iotago.SlotIndex, iotago.BlockID, *blocks.Block](),
		slotData:               shrinkingmap.New[iotago.SlotIndex, *slotData](),
		accountsStatePerSlot:   shrinkingmap.New[iotago.SlotIndex, map[iotago.AccountID]*AccountState](),
		latestFieldsPerAccount: shrinkingmap.New[iotago.AccountID, *latestAccountFields](),
	}

	t.Instance = t.initAccountLedger()

	return t
}

func (t *TestSuite) initAccountLedger() *accountsledger.Manager {
	prunableStores := make(map[iotago.SlotIndex]kvstore.KVStore)
	slotDiffFunc := func(index iotago.SlotIndex) *prunable.AccountDiffs {
		if _, exists := prunableStores[index]; !exists {
			prunableStores[index] = mapdb.NewMapDB()
		}

		p := prunable.NewAccountDiffs(index, prunableStores[index], tpkg.TestAPI)

		return p
	}

	blockFunc := func(id iotago.BlockID) (*blocks.Block, bool) {
		storage := t.blocks.Get(id.Index())
		if storage == nil {
			return nil, false
		}

		return storage.Get(id)
	}

	manager := accountsledger.New(blockFunc, slotDiffFunc, mapdb.NewMapDB())
	manager.SetCommitmentEvictionAge(tpkg.TestAPI.ProtocolParameters().EvictionAge() << 1)

	return manager
}

func (t *TestSuite) ApplySlotActions(slotIndex iotago.SlotIndex, actions map[string]*AccountActions) {
	slotDetails := newSlotData()
	t.slotData.Set(slotIndex, slotDetails)

	// Commit an empty diff if no actions specified.
	if len(actions) == 0 {
		err := t.Instance.ApplyDiff(slotIndex, make(map[iotago.AccountID]*prunable.AccountDiff), advancedset.New[iotago.AccountID]())
		require.NoError(t.T, err)
		return
	}

	// Prepare the slot diff for each account based on the given actions.
	for alias, action := range actions {
		accountID := t.AccountID(alias, true)

		// Apply the burns to the manager.
		slotDetails.Burns[accountID] = lo.Sum(action.Burns...)
		for _, burn := range action.Burns {
			block := t.createBlockWithBurn(accountID, slotIndex, burn)
			t.blocks.Get(slotIndex, true).Set(block.ID(), block)
			t.Instance.TrackBlock(block)
		}

		if action.Destroyed {
			slotDetails.DestroyedAccounts.Add(accountID)
		}

		prevAccountFields, exists := t.latestFieldsPerAccount.Get(accountID)
		if !exists {
			prevAccountFields = &latestAccountFields{
				OutputID:       iotago.EmptyOutputID,
				BICUpdatedAt:   0,
				UpdatedInSlots: advancedset.New[iotago.SlotIndex](),
			}
			t.latestFieldsPerAccount.Set(accountID, prevAccountFields)
		}
		prevAccountFields.UpdatedInSlots.Add(slotIndex)

		// Put everything together in the format that the manager expects.
		slotDetails.SlotDiff[accountID] = &prunable.AccountDiff{
			BICChange:           iotago.BlockIssuanceCredits(action.TotalAllotments), // manager takes AccountDiff only with allotments filled in when applyDiff is triggered
			PubKeysAdded:        t.PublicKeys(action.AddedKeys, true),
			PubKeysRemoved:      t.PublicKeys(action.RemovedKeys, true),
			PreviousUpdatedTime: prevAccountFields.BICUpdatedAt,

			DelegationStakeChange: action.DelegationStakeChange,
			ValidatorStakeChange:  action.ValidatorStakeChange,
			StakeEndEpochChange:   action.StakeEndEpochChange,
			FixedCostChange:       action.FixedCostChange,
		}

		if action.TotalAllotments+lo.Sum(action.Burns...) != 0 || !exists {
			prevAccountFields.BICUpdatedAt = slotIndex
		}

		// If an output ID is specified, we need to update the latest output ID for the account as we transitioned it within this slot.
		// Account creation and transition.
		if action.NewOutputID != "" {
			outputID := t.OutputID(action.NewOutputID, true)
			slotDiff := slotDetails.SlotDiff[accountID]

			slotDiff.NewOutputID = outputID
			slotDiff.PreviousOutputID = prevAccountFields.OutputID

			prevAccountFields.OutputID = outputID

		} else if action.StakeEndEpochChange != 0 || action.ValidatorStakeChange != 0 || action.FixedCostChange != 0 {
			panic("need to update outputID when updating stake end epoch or staking change")
		}

		// Account destruction.
		if prevAccountFields.OutputID != iotago.EmptyOutputID && slotDetails.SlotDiff[accountID].NewOutputID == iotago.EmptyOutputID {
			slotDetails.SlotDiff[accountID].PreviousOutputID = prevAccountFields.OutputID
		}

		// Sanity check for an incorrect case.
		if prevAccountFields.OutputID == iotago.EmptyOutputID && slotDetails.SlotDiff[accountID].NewOutputID == iotago.EmptyOutputID {
			panic("previous account OutputID in the local map and NewOutputID in slot diff cannot be empty")
		}

		fmt.Printf("prepared state diffs %+v\nprevAccountdata %+v\n", slotDetails.SlotDiff[accountID], prevAccountFields)
	}

	// Clone the diffs to prevent the manager from modifying it.
	diffs := make(map[iotago.AccountID]*prunable.AccountDiff)
	for accountID, diff := range slotDetails.SlotDiff {
		diffs[accountID] = diff.Clone()
	}

	err := t.Instance.ApplyDiff(slotIndex, diffs, slotDetails.DestroyedAccounts.Clone())
	require.NoError(t.T, err)
}

func (t *TestSuite) createBlockWithBurn(accountID iotago.AccountID, index iotago.SlotIndex, burn iotago.Mana) *blocks.Block {
	innerBlock := tpkg.RandBasicBlockWithIssuerAndBurnedMana(accountID, burn)
	innerBlock.IssuingTime = tpkg.TestAPI.TimeProvider().SlotStartTime(index)
	modelBlock, err := model.BlockFromBlock(innerBlock, tpkg.TestAPI)

	require.NoError(t.T, err)

	return blocks.NewBlock(modelBlock)
}

func (t *TestSuite) AssertAccountLedgerUntilWithoutNewState(slotIndex iotago.SlotIndex) {
	// Assert the state for each slot.
	for i := iotago.SlotIndex(1); i <= slotIndex; i++ {
		storedAccountsState, exists := t.accountsStatePerSlot.Get(i)
		require.True(t.T, exists, "accountsStatePerSlot should exist for slot %d should exist", i)

		for accountID, expectedState := range storedAccountsState {
			t.assertAccountState(i, accountID, expectedState)
			t.assertDiff(i, accountID, expectedState)
		}
	}
}

func (t *TestSuite) AssertAccountLedgerUntil(slotIndex iotago.SlotIndex, accountsState map[string]*AccountState) {
	expectedAccountsStateForSlot := make(map[iotago.AccountID]*AccountState)
	t.accountsStatePerSlot.Set(slotIndex, expectedAccountsStateForSlot)

	// Populate accountsStatePerSlot with the expected state for the given slot.
	for alias, expectedState := range accountsState {
		accountID := t.AccountID(alias, false)
		expectedAccountsStateForSlot[accountID] = expectedState
	}

	t.AssertAccountLedgerUntilWithoutNewState(slotIndex)
}

func (t *TestSuite) assertAccountState(slotIndex iotago.SlotIndex, accountID iotago.AccountID, expectedState *AccountState) {
	expectedPubKeys := advancedset.New(t.PublicKeys(expectedState.PubKeys, false)...)
	expectedCredits := accounts.NewBlockIssuanceCredits(iotago.BlockIssuanceCredits(expectedState.BICAmount), expectedState.BICUpdatedTime)

	actualState, exists, err := t.Instance.Account(accountID, slotIndex)
	require.NoError(t.T, err)

	if expectedState.Destroyed {
		require.False(t.T, exists)

		return
	}

	require.True(t.T, exists)

	require.Equal(t.T, accountID, actualState.ID)
	require.Equal(t.T, expectedCredits, actualState.Credits, "slotIndex: %d, accountID %s: expected: %v, actual: %v", slotIndex, accountID, expectedCredits, actualState.Credits)
	require.Truef(t.T, expectedPubKeys.Equal(actualState.PubKeys), "slotIndex: %d, accountID %s: expected: %s, actual: %s", slotIndex, accountID, expectedPubKeys, actualState.PubKeys)

	require.Equal(t.T, t.OutputID(expectedState.OutputID, false), actualState.OutputID)

	require.Equal(t.T, expectedState.StakeEndEpoch, actualState.StakeEndEpoch, "slotIndex: %d, accountID %s: expected StakeEndEpoch: %d, actual: %d", slotIndex, accountID, expectedState.StakeEndEpoch, actualState.StakeEndEpoch)
	require.Equal(t.T, expectedState.ValidatorStake, actualState.ValidatorStake, "slotIndex: %d, accountID %s: expected ValidatorStake: %d, actual: %d", slotIndex, accountID, expectedState.ValidatorStake, actualState.ValidatorStake)
	require.Equal(t.T, expectedState.FixedCost, actualState.FixedCost, "slotIndex: %d, accountID %s: expected FixedCost: %d, actual: %d", slotIndex, accountID, expectedState.FixedCost, actualState.FixedCost)
	require.Equal(t.T, expectedState.DelegationStake, actualState.DelegationStake, "slotIndex: %d, accountID %s: expected DelegationStake: %d, actual: %d", slotIndex, accountID, expectedState.DelegationStake, actualState.DelegationStake)
}

func (t *TestSuite) assertDiff(slotIndex iotago.SlotIndex, accountID iotago.AccountID, expectedState *AccountState) {
	actualDiff, destroyed, err := t.Instance.LoadSlotDiff(slotIndex, accountID)
	if !lo.Return1(t.latestFieldsPerAccount.Get(accountID)).UpdatedInSlots.Has(slotIndex) {
		require.Errorf(t.T, err, "expected error for account %s at slot %d", accountID, slotIndex)
		return
	}
	require.NoError(t.T, err)

	accountsSlotBuildData, exists := t.slotData.Get(slotIndex)
	require.True(t.T, exists)
	expectedAccountDiff := accountsSlotBuildData.SlotDiff[accountID]

	require.Equal(t.T, expectedAccountDiff.PreviousOutputID, actualDiff.PreviousOutputID)
	require.Equal(t.T, expectedAccountDiff.PreviousUpdatedTime, actualDiff.PreviousUpdatedTime)

	if expectedState.Destroyed {
		require.Equal(t.T, expectedState.Destroyed, destroyed)
		require.True(t.T, accountsSlotBuildData.DestroyedAccounts.Has(accountID))

		if slotIndex > 1 {
			previousAccountState, exists := t.accountsStatePerSlot.Get(slotIndex - 1)
			require.True(t.T, exists)

			require.Equal(t.T, t.PublicKeys(previousAccountState[accountID].PubKeys, false), actualDiff.PubKeysRemoved)
			require.Equal(t.T, -iotago.BlockIssuanceCredits(previousAccountState[accountID].BICAmount), actualDiff.BICChange)
			require.Equal(t.T, iotago.EmptyOutputID, actualDiff.NewOutputID)

			return
		}
	}

	require.Equal(t.T, expectedAccountDiff.NewOutputID, actualDiff.NewOutputID)
	require.Equal(t.T, expectedAccountDiff.BICChange-iotago.BlockIssuanceCredits(accountsSlotBuildData.Burns[accountID]), actualDiff.BICChange)
	require.Equal(t.T, expectedAccountDiff.PubKeysAdded, actualDiff.PubKeysAdded)
	require.Equal(t.T, expectedAccountDiff.PubKeysRemoved, actualDiff.PubKeysRemoved)
}

func (t *TestSuite) AccountID(alias string, createIfNotExists bool) iotago.AccountID {
	if accID, exists := t.accounts[alias]; exists {
		return accID
	} else if !createIfNotExists {
		panic(fmt.Sprintf("account with alias '%s' does not exist", alias))
	}

	t.accounts[alias] = tpkg.RandAccountID()
	t.accounts[alias].RegisterAlias(alias)

	return t.accounts[alias]
}

func (t *TestSuite) OutputID(alias string, createIfNotExists bool) iotago.OutputID {
	if outputID, exists := t.outputs[alias]; exists {
		return outputID
	} else if !createIfNotExists {
		panic(fmt.Sprintf("output with alias '%s' does not exist", alias))
	}
	t.outputs[alias] = tpkg.RandOutputID(1)

	return t.outputs[alias]
}

func (t *TestSuite) PublicKey(alias string, createIfNotExists bool) ed25519.PublicKey {
	if pubKey, exists := t.pubKeys[alias]; exists {
		return pubKey
	} else if !createIfNotExists {
		panic(fmt.Sprintf("public key with alias '%s' does not exist", alias))
	}

	t.pubKeys[alias] = utils.RandPubKey()

	return t.pubKeys[alias]
}

func (t *TestSuite) PublicKeys(pubKeys []string, createIfNotExists bool) []ed25519.PublicKey {
	keys := make([]ed25519.PublicKey, len(pubKeys))
	for i, pubKey := range pubKeys {
		keys[i] = t.PublicKey(pubKey, createIfNotExists)
	}

	return keys
}

type AccountActions struct {
	TotalAllotments iotago.Mana
	Burns           []iotago.Mana
	Destroyed       bool
	AddedKeys       []string
	RemovedKeys     []string

	ValidatorStakeChange int64
	StakeEndEpochChange  int64
	FixedCostChange      int64

	DelegationStakeChange int64

	NewOutputID string
}

type AccountState struct {
	BICAmount      iotago.Mana
	BICUpdatedTime iotago.SlotIndex

	OutputID string
	PubKeys  []string

	ValidatorStake  iotago.BaseToken
	DelegationStake iotago.BaseToken
	FixedCost       iotago.Mana
	StakeEndEpoch   iotago.EpochIndex

	Destroyed bool
}

type latestAccountFields struct {
	OutputID       iotago.OutputID
	BICUpdatedAt   iotago.SlotIndex
	UpdatedInSlots *advancedset.AdvancedSet[iotago.SlotIndex]
}

type slotData struct {
	Burns             map[iotago.AccountID]iotago.Mana
	SlotDiff          map[iotago.AccountID]*prunable.AccountDiff
	DestroyedAccounts *advancedset.AdvancedSet[iotago.AccountID]
}

func newSlotData() *slotData {
	return &slotData{
		DestroyedAccounts: advancedset.New[iotago.AccountID](),
		Burns:             make(map[iotago.AccountID]iotago.Mana),
		SlotDiff:          make(map[iotago.AccountID]*prunable.AccountDiff),
	}
}