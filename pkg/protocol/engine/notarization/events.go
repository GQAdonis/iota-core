package notarization

import (
	"github.com/iotaledger/hive.go/ds"
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/iota-core/pkg/model"
	iotago "github.com/iotaledger/iota.go/v4"
)

// Events is a container that acts as a dictionary for the events of the notarization manager.
type Events struct {
	SlotCommitted *event.Event1[*SlotCommittedDetails]

	event.Group[Events, *Events]
}

// NewEvents contains the constructor of the Events object (it is generated by a generic factory).
var NewEvents = event.CreateGroupConstructor(func() (self *Events) {
	return &Events{
		SlotCommitted: event.New1[*SlotCommittedDetails](),
	}
})

// SlotCommittedDetails contains the details of a committed slot.
type SlotCommittedDetails struct {
	Commitment            *model.Commitment
	AcceptedBlocks        ds.AuthenticatedSet[iotago.BlockID]
	ActiveValidatorsCount int
}
