package debugapi

import (
	"sort"

	"github.com/iotaledger/hive.go/ds"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	iotago "github.com/iotaledger/iota.go/v4"
)

func getSlotBlockIDs(index iotago.SlotIndex) (*BlockChangesResponse, error) {
	blocksForSlot := deps.Protocol.MainEngineInstance().Storage.Blocks(index)
	if blocksForSlot == nil {
		return nil, ierrors.Errorf("cannot find block storage bucket for slot %d", index)
	}

	includedBlocks := make([]string, 0)
	tangleTree := ds.NewAuthenticatedSet(mapdb.NewMapDB(), iotago.SlotIdentifier.Bytes, iotago.SlotIdentifierFromBytes)

	_ = blocksForSlot.ForEachBlockIDInSlot(func(blockID iotago.BlockID) error {
		includedBlocks = append(includedBlocks, blockID.String())
		tangleTree.Add(blockID)

		return nil
	})

	sort.Strings(includedBlocks)

	return &BlockChangesResponse{
		Index:          index,
		IncludedBlocks: includedBlocks,
		TangleRoot:     iotago.Identifier(tangleTree.Root()).String(),
	}, nil
}
