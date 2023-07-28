package sybilprotection

import (
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/iota-core/pkg/core/account"
	iotago "github.com/iotaledger/iota.go/v4"
)

type Events struct {
	CommitteeSelected *event.Event2[*account.Accounts, iotago.EpochIndex]
	RewardsCommitted  *event.Event1[iotago.EpochIndex]

	event.Group[Events, *Events]
}

// NewEvents contains the constructor of the Events object (it is generated by a generic factory).
var NewEvents = event.CreateGroupConstructor(func() (newEvents *Events) {
	return &Events{
		CommitteeSelected: event.New2[*account.Accounts, iotago.EpochIndex](),
		RewardsCommitted:  event.New1[iotago.EpochIndex](),
	}
})