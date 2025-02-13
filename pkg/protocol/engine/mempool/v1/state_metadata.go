package mempoolv1

import (
	"sync/atomic"

	"github.com/iotaledger/hive.go/ds/reactive"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/hive.go/runtime/promise"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/mempool"
	iotago "github.com/iotaledger/iota.go/v4"
)

type StateMetadata struct {
	state mempool.State

	// lifecycle
	spenderCount       uint64
	spent              *promise.Event
	doubleSpent        *promise.Event
	spendAccepted      reactive.Variable[*TransactionMetadata]
	spendCommitted     reactive.Variable[*TransactionMetadata]
	inclusionSlot      reactive.Variable[*iotago.SlotIndex]
	allSpendersRemoved *event.Event

	spenderIDs reactive.DerivedSet[iotago.TransactionID]

	*inclusionFlags
}

func NewStateMetadata(state mempool.State, optSource ...*TransactionMetadata) *StateMetadata {
	return (&StateMetadata{
		state: state,

		spent:              promise.NewEvent(),
		doubleSpent:        promise.NewEvent(),
		spendAccepted:      reactive.NewVariable[*TransactionMetadata](),
		spendCommitted:     reactive.NewVariable[*TransactionMetadata](),
		inclusionSlot:      reactive.NewVariable[*iotago.SlotIndex](),
		allSpendersRemoved: event.New(),

		spenderIDs: reactive.NewDerivedSet[iotago.TransactionID](),

		inclusionFlags: newInclusionFlags(),
	}).setup(optSource...)
}

func (s *StateMetadata) setup(optSource ...*TransactionMetadata) *StateMetadata {
	if len(optSource) == 0 {
		return s
	}
	source := optSource[0]

	s.spenderIDs.InheritFrom(source.spenderIDs)

	source.earliestIncludedValidAttachment.OnUpdate(func(_, newValue iotago.BlockID) {
		s.inclusionSlot.Compute(func(currentValue *iotago.SlotIndex) *iotago.SlotIndex {
			if newSlot := newValue.Slot(); currentValue == nil || newSlot < *currentValue {
				return &newSlot
			}

			return currentValue
		})
	})

	source.OnAccepted(func() { s.accepted.Set(true) })
	source.OnRejected(func() { s.rejected.Trigger() })
	source.OnCommittedSlotUpdated(lo.Void(s.committedSlot.Set))
	source.OnOrphanedSlotUpdated(lo.Void(s.orphanedSlot.Set))

	return s
}

func (s *StateMetadata) State() mempool.State {
	return s.state
}

func (s *StateMetadata) SpenderIDs() reactive.Set[iotago.TransactionID] {
	return s.spenderIDs
}

func (s *StateMetadata) IsDoubleSpent() bool {
	return s.doubleSpent.WasTriggered()
}

func (s *StateMetadata) OnDoubleSpent(callback func()) {
	s.doubleSpent.OnTrigger(callback)
}

func (s *StateMetadata) AcceptedSpender() (mempool.TransactionMetadata, bool) {
	acceptedSpender := s.spendAccepted.Get()

	return acceptedSpender, acceptedSpender != nil
}

func (s *StateMetadata) OnAcceptedSpenderUpdated(callback func(spender mempool.TransactionMetadata)) {
	s.spendAccepted.OnUpdate(func(prevValue *TransactionMetadata, newValue *TransactionMetadata) {
		if prevValue != newValue {
			callback(newValue)
		}
	})
}

func (s *StateMetadata) OnSpendCommitted(callback func(spender mempool.TransactionMetadata)) {
	s.spendCommitted.OnUpdate(func(prevValue *TransactionMetadata, newValue *TransactionMetadata) {
		if prevValue != newValue {
			callback(newValue)
		}
	})
}

func (s *StateMetadata) AllSpendersRemoved() bool {
	return s.allSpendersRemoved.WasTriggered()
}

func (s *StateMetadata) onAllSpendersRemoved(callback func()) {
	s.allSpendersRemoved.Hook(callback)
}

func (s *StateMetadata) PendingSpenderCount() int {
	return int(atomic.LoadUint64(&s.spenderCount))
}

func (s *StateMetadata) HasNoSpenders() bool {
	return atomic.LoadUint64(&s.spenderCount) == 0
}

func (s *StateMetadata) InclusionSlot() iotago.SlotIndex {
	return *s.inclusionSlot.Get()
}

func (s *StateMetadata) OnInclusionSlotUpdated(callback func(prevID iotago.SlotIndex, newID iotago.SlotIndex)) {
	s.inclusionSlot.OnUpdate(func(oldValue *iotago.SlotIndex, newValue *iotago.SlotIndex) {
		switch {
		case oldValue == nil:
			callback(iotago.SlotIndex(0), *newValue)
		case newValue == nil:
			callback(*oldValue, iotago.SlotIndex(0))
		default:
			callback(*oldValue, *newValue)
		}
	})
}

func (s *StateMetadata) increaseSpenderCount() {
	if spenderCount := atomic.AddUint64(&s.spenderCount, 1); spenderCount == 1 {
		s.spent.Trigger()
	} else if spenderCount == 2 {
		s.doubleSpent.Trigger()
	}
}

func (s *StateMetadata) decreaseSpenderCount() {
	if atomic.AddUint64(&s.spenderCount, ^uint64(0)) == 0 {
		s.allSpendersRemoved.Trigger()
	}
}

func (s *StateMetadata) setupSpender(spender *TransactionMetadata) {
	s.increaseSpenderCount()

	spender.OnAccepted(func() {
		if !s.state.IsReadOnly() {
			s.spendAccepted.Set(spender)
		}
	})

	spender.OnCommittedSlotUpdated(func(_ iotago.SlotIndex) {
		if !s.state.IsReadOnly() {
			s.spendCommitted.Set(spender)
		}

		s.decreaseSpenderCount()
	})

	spender.OnOrphanedSlotUpdated(func(_ iotago.SlotIndex) { s.decreaseSpenderCount() })
}
