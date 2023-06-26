package blocktime

import (
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/hive.go/runtime/module"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/protocol/engine"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/clock"
	iotago "github.com/iotaledger/iota.go/v4"
)

// Clock implements the clock.Clock interface that sources its notion of time from accepted and confirmed blocks.
type Clock struct {
	// acceptedTime contains a notion of time that is anchored to the latest accepted block.
	acceptedTime *RelativeTime

	// confirmedTime contains a notion of time that is anchored to the latest confirmed block.
	confirmedTime *RelativeTime

	// Module embeds the required methods of the module.Interface.
	module.Module
}

// NewProvider creates a new Clock provider with the given options.
func NewProvider(opts ...options.Option[Clock]) module.Provider[*engine.Engine, clock.Clock] {
	return module.Provide(func(e *engine.Engine) clock.Clock {
		return options.Apply(&Clock{
			acceptedTime:  NewRelativeTime(),
			confirmedTime: NewRelativeTime(),
		}, opts, func(c *Clock) {
			e.HookConstructed(func() {
				e.Storage.Settings().HookInitialized(func() {
					c.acceptedTime.Set(e.API().TimeProvider().SlotEndTime(e.Storage.Settings().LatestCommitment().Index()))
					c.confirmedTime.Set(e.API().TimeProvider().SlotEndTime(e.Storage.Settings().LatestFinalizedSlot()))

					c.TriggerInitialized()
				})

				e.Events.Clock.AcceptedTimeUpdated.LinkTo(c.acceptedTime.OnUpdated)
				e.Events.Clock.ConfirmedTimeUpdated.LinkTo(c.confirmedTime.OnUpdated)

				asyncOpt := event.WithWorkerPool(e.Workers.CreatePool("Clock", 1))
				c.HookStopped(lo.Batch(
					e.Events.BlockGadget.BlockAccepted.Hook(func(block *blocks.Block) {
						c.acceptedTime.Advance(block.IssuingTime())
					}, asyncOpt).Unhook,

					e.Events.BlockGadget.BlockConfirmed.Hook(func(block *blocks.Block) {
						c.confirmedTime.Advance(block.IssuingTime())
					}, asyncOpt).Unhook,

					e.Events.SlotGadget.SlotFinalized.Hook(func(index iotago.SlotIndex) {
						c.acceptedTime.Advance(e.API().TimeProvider().SlotEndTime(index))
						c.confirmedTime.Advance(e.API().TimeProvider().SlotEndTime(index))
					}, asyncOpt).Unhook,
				))
			})

			e.HookStopped(c.TriggerStopped)
		}, (*Clock).TriggerConstructed)
	})
}

// Accepted returns a notion of time that is anchored to the latest accepted block.
func (c *Clock) Accepted() clock.RelativeTime {
	return c.acceptedTime
}

// Confirmed returns a notion of time that is anchored to the latest confirmed block.
func (c *Clock) Confirmed() clock.RelativeTime {
	return c.confirmedTime
}

func (c *Clock) Shutdown() {
	c.TriggerStopped()
}
