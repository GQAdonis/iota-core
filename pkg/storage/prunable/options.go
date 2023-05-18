package prunable

import (
	"github.com/iotaledger/hive.go/runtime/options"
)

// WithGranularity sets the granularity of the DB instances (i.e. how many buckets/slots are stored in one DB).
// It thus also has an impact on how fine-grained buckets/slots can be pruned.
func WithGranularity(granularity int64) options.Option[Manager] {
	return func(m *Manager) {
		m.optsGranularity = granularity
	}
}

// WithMaxOpenDBs sets the maximum concurrently open DBs.
func WithMaxOpenDBs(optsMaxOpenDBs int) options.Option[Manager] {
	return func(m *Manager) {
		m.optsMaxOpenDBs = optsMaxOpenDBs
	}
}
