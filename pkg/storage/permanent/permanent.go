package permanent

import (
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/ioutils"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/storage/database"
)

const (
	settingsPrefix byte = iota
	commitmentsPrefix
	sybilProtectionPrefix
	attestationsPrefix
	ledgerPrefix
	accountsPrefix
	latestNonEmptySlotPrefix
	rewardsPrefix
	poolStatsPrefix
	committeePrefix
	upgradeSignalsPrefix
)

type Permanent struct {
	dbConfig      database.Config
	store         kvstore.KVStore
	healthTracker *kvstore.StoreHealthTracker
	errorHandler  func(error)

	settings    *Settings
	commitments *Commitments

	sybilProtection    kvstore.KVStore
	attestations       kvstore.KVStore
	ledger             kvstore.KVStore
	accounts           kvstore.KVStore
	latestNonEmptySlot kvstore.KVStore
	rewards            kvstore.KVStore
	poolStats          kvstore.KVStore
	committee          kvstore.KVStore
	upgradeSignals     kvstore.KVStore
}

// New returns a new permanent storage instance.
func New(dbConfig database.Config, errorHandler func(error), opts ...options.Option[Permanent]) *Permanent {
	return options.Apply(&Permanent{
		errorHandler: errorHandler,
	}, opts, func(p *Permanent) {
		var err error
		p.store, err = database.StoreWithDefaultSettings(dbConfig.Directory, true, dbConfig.Engine)
		if err != nil {
			panic(err)
		}

		p.healthTracker, err = kvstore.NewStoreHealthTracker(p.store, dbConfig.PrefixHealth, dbConfig.Version, nil)
		if err != nil {
			panic(ierrors.Wrapf(err, "database in %s is corrupted, delete database and resync node", dbConfig.Directory))
		}
		if err = p.healthTracker.MarkCorrupted(); err != nil {
			panic(err)
		}

		p.settings = NewSettings(lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{settingsPrefix})))
		p.commitments = NewCommitments(lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{commitmentsPrefix})), p.settings)
		p.sybilProtection = lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{sybilProtectionPrefix}))
		p.attestations = lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{attestationsPrefix}))
		p.ledger = lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{ledgerPrefix}))
		p.accounts = lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{accountsPrefix}))
		p.latestNonEmptySlot = lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{latestNonEmptySlotPrefix}))
		p.rewards = lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{rewardsPrefix}))
		p.poolStats = lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{poolStatsPrefix}))
		p.committee = lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{committeePrefix}))
		p.upgradeSignals = lo.PanicOnErr(p.store.WithExtendedRealm(kvstore.Realm{upgradeSignalsPrefix}))
	})
}

func (p *Permanent) Settings() *Settings {
	return p.settings
}

func (p *Permanent) Commitments() *Commitments {
	return p.commitments
}

// SybilProtection returns the sybil protection storage (or a specialized sub-storage if a realm is provided).
func (p *Permanent) SybilProtection(optRealm ...byte) kvstore.KVStore {
	if len(optRealm) == 0 {
		return p.sybilProtection
	}

	return lo.PanicOnErr(p.sybilProtection.WithExtendedRealm(optRealm))
}

// Accounts returns the Accounts storage (or a specialized sub-storage if a realm is provided).
func (p *Permanent) Accounts(optRealm ...byte) kvstore.KVStore {
	if len(optRealm) == 0 {
		return p.accounts
	}

	return lo.PanicOnErr(p.accounts.WithExtendedRealm(optRealm))
}

func (p *Permanent) LatestNonEmptySlot(optRealm ...byte) kvstore.KVStore {
	if len(optRealm) == 0 {
		return p.accounts
	}

	return lo.PanicOnErr(p.latestNonEmptySlot.WithExtendedRealm(optRealm))
}

// TODO: Rewards and PoolStats should be pruned after one year, so they are not really permanent.

// Rewards returns the Rewards storage (or a specialized sub-storage if a realm is provided).
func (p *Permanent) Rewards(optRealm ...byte) kvstore.KVStore {
	if len(optRealm) == 0 {
		return p.rewards
	}

	return lo.PanicOnErr(p.rewards.WithExtendedRealm(optRealm))
}

// PoolStats returns the PoolStats storage.
func (p *Permanent) PoolStats(optRealm ...byte) kvstore.KVStore {
	if len(optRealm) == 0 {
		return p.poolStats
	}

	return lo.PanicOnErr(p.poolStats.WithExtendedRealm(optRealm))
}

// UpgradeSignals returns the UpgradeSignals storage.
// TODO: this can be pruned after 7 epochs, so it is not really permanent.
func (p *Permanent) UpgradeSignals(optRealm ...byte) kvstore.KVStore {
	if len(optRealm) == 0 {
		return p.upgradeSignals
	}

	return lo.PanicOnErr(p.upgradeSignals.WithExtendedRealm(optRealm))
}

func (p *Permanent) Committee(optRealm ...byte) kvstore.KVStore {
	if len(optRealm) == 0 {
		return p.committee
	}

	return lo.PanicOnErr(p.committee.WithExtendedRealm(optRealm))
}

// Attestations returns the "attestations" storage (or a specialized sub-storage if a realm is provided).
func (p *Permanent) Attestations(optRealm ...byte) kvstore.KVStore {
	if len(optRealm) == 0 {
		return p.attestations
	}

	return lo.PanicOnErr(p.attestations.WithExtendedRealm(optRealm))
}

// Ledger returns the ledger storage (or a specialized sub-storage if a realm is provided).
func (p *Permanent) Ledger(optRealm ...byte) kvstore.KVStore {
	if len(optRealm) == 0 {
		return p.ledger
	}

	return lo.PanicOnErr(p.ledger.WithExtendedRealm(optRealm))
}

// Size returns the size of the permanent storage.
func (p *Permanent) Size() int64 {
	dbSize, err := ioutils.FolderSize(p.dbConfig.Directory)
	if err != nil {
		p.errorHandler(ierrors.Wrapf(err, "dbDirectorySize failed for %s", p.dbConfig.Directory))
		return 0
	}

	return dbSize
}

func (p *Permanent) Shutdown() {
	if err := p.healthTracker.MarkHealthy(); err != nil {
		panic(err)
	}
	if err := database.FlushAndClose(p.store); err != nil {
		panic(err)
	}
}
