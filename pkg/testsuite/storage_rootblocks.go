package testsuite

import (
	"github.com/pkg/errors"

	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
)

func (t *TestSuite) AssertStorageRootBlocks(blocks []*model.Block, nodes ...*mock.Node) {
	mustNodes(nodes)

	for _, node := range nodes {
		for _, block := range blocks {
			t.Eventually(func() error {
				storage := node.Protocol.MainEngineInstance().Storage.RootBlocks(block.ID().Index())
				if storage == nil {
					return errors.Errorf("AssertStorageRootBlocks: %s: storage for %s is nil", node.Name, block.ID().Index())
				}

				loadedBlockID, loadedCommitmentID, err := storage.Load(block.ID())
				if err != nil {
					return errors.Wrapf(err, "AssertStorageRootBlocks: %s: failed to load root block %s", node.Name, block.ID())
				}

				if block.ID() != loadedBlockID {
					return errors.Errorf("AssertStorageRootBlocks: %s: expected block %s, got %s", node.Name, block.ID(), loadedBlockID)
				}

				if block.SlotCommitment().ID() != loadedCommitmentID {
					return errors.Errorf("AssertStorageRootBlocks: %s: expected slot commitment %s, got %s", node.Name, block.SlotCommitment().ID(), loadedCommitmentID)
				}

				return nil
			})
		}
	}
}
