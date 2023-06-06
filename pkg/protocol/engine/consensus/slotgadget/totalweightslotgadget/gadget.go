package totalweightslotgadget

import (
	"sync"

	"github.com/pkg/errors"

	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/hive.go/runtime/module"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/hive.go/runtime/workerpool"
	"github.com/iotaledger/iota-core/pkg/protocol/engine"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/consensus/slotgadget"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/sybilprotection"
	"github.com/iotaledger/iota-core/pkg/votes"
	"github.com/iotaledger/iota-core/pkg/votes/slottracker"
	iotago "github.com/iotaledger/iota.go/v4"
)

type Gadget struct {
	events  *slotgadget.Events
	workers *workerpool.Group

	// Keep track of votes on slots (from commitments) per slot of blocks. I.e. a slot can only be finalized if
	// optsSlotFinalizationThreshold is reached within a slot.
	slotTrackers    *shrinkingmap.ShrinkingMap[iotago.SlotIndex, *slottracker.SlotTracker]
	sybilProtection sybilprotection.SybilProtection

	lastFinalizedSlot          iotago.SlotIndex
	storeLastFinalizedSlotFunc func(index iotago.SlotIndex)

	mutex        sync.RWMutex
	errorHandler func(error)

	optsSlotFinalizationThreshold float64

	module.Module
}

func NewProvider(opts ...options.Option[Gadget]) module.Provider[*engine.Engine, slotgadget.Gadget] {
	return module.Provide(func(e *engine.Engine) slotgadget.Gadget {
		return options.Apply(&Gadget{
			events:                        slotgadget.NewEvents(),
			optsSlotFinalizationThreshold: 0.67,
			errorHandler:                  e.ErrorHandler("slotgadget"),
		}, opts, func(g *Gadget) {
			g.sybilProtection = e.SybilProtection
			g.slotTrackers = shrinkingmap.New[iotago.SlotIndex, *slottracker.SlotTracker]()

			e.Events.SlotGadget.LinkTo(g.events)
			g.workers = e.Workers.CreateGroup("SlotGadget")

			e.Events.BlockGadget.BlockRatifiedConfirmed.Hook(g.trackVotes, event.WithWorkerPool(g.workers.CreatePool("TrackAndRefresh", 1))) // Using just 1 worker to avoid contention

			g.storeLastFinalizedSlotFunc = func(index iotago.SlotIndex) {
				if err := e.Storage.Settings().SetLatestFinalizedSlot(index); err != nil {
					g.errorHandler(errors.Wrap(err, "failed to set latest finalized slot"))
				}
			}

			e.HookInitialized(func() {
				// Can't use setter here as it has a side effect.
				func() {
					g.mutex.Lock()
					defer g.mutex.Unlock()
					g.lastFinalizedSlot = e.Storage.Permanent.Settings().LatestFinalizedSlot()
				}()

				g.TriggerInitialized()
			})
		},
			(*Gadget).TriggerConstructed,
		)
	})
}

func (g *Gadget) Shutdown() {
	g.TriggerStopped()
	g.workers.Shutdown()
}

func (g *Gadget) setLastFinalizedSlot(i iotago.SlotIndex) {
	g.lastFinalizedSlot = i
	g.storeLastFinalizedSlotFunc(i)
}

func (g *Gadget) trackVotes(block *blocks.Block) {
	finalizedSlots := func() []iotago.SlotIndex {
		g.mutex.Lock()
		defer g.mutex.Unlock()

		tracker, _ := g.slotTrackers.GetOrCreate(block.ID().Index(), func() *slottracker.SlotTracker {
			return slottracker.NewSlotTracker()
		})

		prevLatestSlot, latestSlot, updated := tracker.TrackVotes(block.SlotCommitmentID().Index(), block.Block().IssuerID, g.lastFinalizedSlot)
		if !updated {
			return nil
		}

		return g.refreshSlotFinalization(tracker, prevLatestSlot, latestSlot)
	}()

	for _, finalizedSlot := range finalizedSlots {
		g.events.SlotFinalized.Trigger(finalizedSlot)

		g.slotTrackers.Delete(finalizedSlot)
	}
}

func (g *Gadget) refreshSlotFinalization(tracker *slottracker.SlotTracker, previousLatestSlotIndex iotago.SlotIndex, newLatestSlotIndex iotago.SlotIndex) (finalizedSlots []iotago.SlotIndex) {
	committee := g.sybilProtection.Committee()
	committeeTotalWeight := committee.TotalWeight()

	for i := lo.Max(g.lastFinalizedSlot, previousLatestSlotIndex) + 1; i <= newLatestSlotIndex; i++ {
		attestorsTotalWeight := committee.SelectAccounts(tracker.Voters(i)...).TotalWeight()

		if !votes.IsThresholdReached(attestorsTotalWeight, committeeTotalWeight, g.optsSlotFinalizationThreshold) {
			break
		}

		g.setLastFinalizedSlot(i)

		finalizedSlots = append(finalizedSlots, i)
	}

	return finalizedSlots
}

var _ slotgadget.Gadget = new(Gadget)
