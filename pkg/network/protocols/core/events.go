package core

import (
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/network"
	iotago "github.com/iotaledger/iota.go/v4"
)

type Events struct {
	BlockReceived                 *event.Event2[*model.Block, network.PeerID]
	BlockRequestReceived          *event.Event2[iotago.BlockID, network.PeerID]
	SlotCommitmentReceived        *event.Event2[*model.Commitment, network.PeerID]
	SlotCommitmentRequestReceived *event.Event2[iotago.CommitmentID, network.PeerID]
	AttestationsReceived          *event.Event4[*model.Commitment, []*iotago.Attestation, iotago.Identifier, network.PeerID]
	AttestationsRequestReceived   *event.Event2[iotago.CommitmentID, network.PeerID]
	Error                         *event.Event2[error, network.PeerID]

	event.Group[Events, *Events]
}

// NewEvents contains the constructor of the Events object (it is generated by a generic factory).
var NewEvents = event.CreateGroupConstructor(func() (newEvents *Events) {
	return &Events{
		BlockReceived:                 event.New2[*model.Block, network.PeerID](),
		BlockRequestReceived:          event.New2[iotago.BlockID, network.PeerID](),
		SlotCommitmentReceived:        event.New2[*model.Commitment, network.PeerID](),
		SlotCommitmentRequestReceived: event.New2[iotago.CommitmentID, network.PeerID](),
		AttestationsReceived:          event.New4[*model.Commitment, []*iotago.Attestation, iotago.Identifier, network.PeerID](),
		AttestationsRequestReceived:   event.New2[iotago.CommitmentID, network.PeerID](),
		Error:                         event.New2[error, network.PeerID](),
	}
})
