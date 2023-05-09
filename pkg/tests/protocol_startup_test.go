package tests

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/protocol"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/notarization/slotnotarization"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/sybilprotection/poa"
	"github.com/iotaledger/iota-core/pkg/testsuite"
	iotago "github.com/iotaledger/iota.go/v4"
)

func TestProtocol_StartNodeFromSnapshotAndDisk(t *testing.T) {
	ts := testsuite.NewTestSuite(t)
	defer ts.Shutdown()

	node1 := ts.AddValidatorNode("node1", 50)
	node2 := ts.AddValidatorNode("node2", 50)

	ts.Run()
	ts.HookLogging()

	ts.Wait()

	expectedCommittee := map[iotago.AccountID]int64{
		node1.AccountID: 50,
		node2.AccountID: 50,
	}

	// Verify that nodes have the expected states.
	ts.AssertNodeState(ts.Nodes(),
		testsuite.WithSnapshotImported(true),
		testsuite.WithProtocolParameters(ts.ProtocolParameters),
		testsuite.WithLatestCommitment(iotago.NewEmptyCommitment()),
		testsuite.WithLatestStateMutationSlot(0),
		testsuite.WithLatestFinalizedSlot(0),
		testsuite.WithChainID(iotago.NewEmptyCommitment().MustID()),
		testsuite.WithStorageCommitments([]*iotago.Commitment{iotago.NewEmptyCommitment()}),
		testsuite.WithSybilProtectionCommittee(expectedCommittee),
		testsuite.WithSybilProtectionOnlineCommittee(expectedCommittee),
		testsuite.WithActiveRootBlocks(ts.Blocks("Genesis")),
		testsuite.WithStorageRootBlocks(ts.Blocks("Genesis")),
	)

	// Issue blocks in subsequent slots and make sure that node state as well as accepted, ratified accepted, and confirmed blocks are correct.
	{
		// Slot 1-2
		{
			// Slot 1
			ts.IssueBlockAtSlot("1.1", 1, iotago.NewEmptyCommitment(), node1, iotago.EmptyBlockID())
			ts.IssueBlockAtSlot("1.2", 1, iotago.NewEmptyCommitment(), node2, iotago.EmptyBlockID())
			ts.IssueBlockAtSlot("1.1*", 1, iotago.NewEmptyCommitment(), node1, ts.BlockID("1.2"))

			// Slot 2
			ts.IssueBlockAtSlot("2.2", 2, iotago.NewEmptyCommitment(), node2, ts.BlockID("1.1"))
			ts.IssueBlockAtSlot("2.2*", 2, iotago.NewEmptyCommitment(), node2, ts.BlockID("1.1*"))

			ts.AssertBlocksExist(ts.Blocks("1.1", "1.2", "1.1*", "2.2", "2.2*"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("1.1", "1.2", "1.1*"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("2.2", "2.2*"), false, ts.Nodes()...)
		}

		// Slot 3-4
		{
			// Slot 3
			ts.IssueBlockAtSlot("3.1", 3, iotago.NewEmptyCommitment(), node1, ts.BlockIDs("2.2", "2.2*")...)

			// Slot 4
			ts.IssueBlockAtSlot("4.2", 4, iotago.NewEmptyCommitment(), node2, ts.BlockID("3.1"))

			ts.AssertBlocksExist(ts.Blocks("3.1", "4.2"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("2.2", "2.2*", "3.1"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("4.2"), false, ts.Nodes()...)

			ts.AssertBlocksInCacheRatifiedAccepted(ts.Blocks("1.1", "1.2", "1.1*"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheConfirmed(ts.Blocks("1.1", "1.2", "1.1*"), true, ts.Nodes()...)
		}

		// Verify nodes' states: Slot 1 should be committed as the MinCommittableSlotAge is 1, and we accepted a block at slot 3.
		ts.AssertNodeState(ts.Nodes(),
			testsuite.WithSnapshotImported(true),
			testsuite.WithProtocolParameters(ts.ProtocolParameters),
			testsuite.WithLatestCommitmentSlotIndex(1),
			testsuite.WithLatestStateMutationSlot(0),
			testsuite.WithLatestFinalizedSlot(0),
			testsuite.WithChainID(iotago.NewEmptyCommitment().MustID()),
			testsuite.WithSybilProtectionCommittee(expectedCommittee),
			testsuite.WithSybilProtectionOnlineCommittee(expectedCommittee),
			testsuite.WithActiveRootBlocks(ts.Blocks("Genesis")),
			testsuite.WithStorageRootBlocks(ts.Blocks("Genesis", "1.1", "1.1*", "2.2", "2.2*")),
		)
		require.Equal(t, node1.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment(), node2.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment())

		// Slot 5-8
		{
			slot1Commitment := lo.PanicOnErr(node1.Protocol.MainEngineInstance().Storage.Commitments().Load(1)).Commitment()

			// Slot 5
			ts.IssueBlockAtSlot("5.1", 5, slot1Commitment, node1, ts.BlockID("4.2"))
			// Slot 6
			ts.IssueBlockAtSlot("6.2", 6, slot1Commitment, node2, ts.BlockID("5.1"))
			// Slot 7
			ts.IssueBlockAtSlot("7.1", 7, slot1Commitment, node1, ts.BlockID("6.2"))
			// Slot 8
			ts.IssueBlockAtSlot("8.2", 8, slot1Commitment, node2, ts.BlockID("7.1"))

			ts.AssertBlocksExist(ts.Blocks("5.1", "6.2", "7.1", "8.2"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("4.2", "5.1", "6.2", "7.1"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("8.2"), false, ts.Nodes()...)

			ts.AssertBlocksInCacheRatifiedAccepted(ts.Blocks("2.2", "2.2*", "3.1", "4.2", "5.1"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheConfirmed(ts.Blocks("2.2", "2.2*", "3.1", "4.2", "5.1"), true, ts.Nodes()...)
		}

		// Verify nodes' states:
		// - Slot 5 should be committed as the MinCommittableSlotAge is 1, and we accepted a block at slot 7.
		// - 5.1 is ratified accepted and commits to slot 1 -> slot 1 should be evicted.
		// - rootblocks are still not evicted as RootBlocksEvictionDelay is 3.
		// - slot 1 is still not finalized: there is no supermajority of ratified accepted blocks that commits to it.
		ts.AssertNodeState(ts.Nodes(),
			testsuite.WithSnapshotImported(true),
			testsuite.WithProtocolParameters(ts.ProtocolParameters),
			testsuite.WithLatestCommitmentSlotIndex(5),
			testsuite.WithLatestStateMutationSlot(0),
			testsuite.WithLatestFinalizedSlot(0),
			testsuite.WithChainID(iotago.NewEmptyCommitment().MustID()),
			testsuite.WithSybilProtectionCommittee(expectedCommittee),
			testsuite.WithSybilProtectionOnlineCommittee(expectedCommittee),
			testsuite.WithActiveRootBlocks(ts.Blocks("Genesis", "1.1", "1.1*")),
			testsuite.WithStorageRootBlocks(ts.Blocks("Genesis", "1.1", "1.1*", "2.2", "2.2*", "3.1", "4.2", "5.1")),
		)
		require.Equal(t, node1.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment(), node2.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment())

		// Slot 9-12
		{
			slot5Commitment := lo.PanicOnErr(node1.Protocol.MainEngineInstance().Storage.Commitments().Load(5)).Commitment()

			// Slot 9
			ts.IssueBlockAtSlot("9.1", 9, slot5Commitment, node1, ts.BlockID("8.2"))
			// Slot 10
			ts.IssueBlockAtSlot("10.2", 10, slot5Commitment, node2, ts.BlockID("9.1"))
			// Slot 11
			ts.IssueBlockAtSlot("11.1", 11, slot5Commitment, node1, ts.BlockID("10.2"))
			// Slot 12
			ts.IssueBlockAtSlot("12.2", 12, slot5Commitment, node2, ts.BlockID("11.1"))

			ts.AssertBlocksExist(ts.Blocks("9.1", "10.2", "11.1", "12.2"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("8.2", "9.1", "11.1"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("12.2"), false, ts.Nodes()...)

			ts.AssertBlocksInCacheRatifiedAccepted(ts.Blocks("6.2", "7.1", "8.2", "9.1"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheConfirmed(ts.Blocks("6.2", "7.1", "8.2", "9.1"), true, ts.Nodes()...)
		}

		// Verify nodes' states:
		// - Slot 9 should be committed as the MinCommittableSlotAge is 1, and we accepted a block at slot 11.
		// - 9.1 is ratified accepted and commits to slot 5 -> slot 5 should be evicted.
		// - rootblocks are evicted until slot 2 as RootBlocksEvictionDelay is 3.
		// - slot 1 is finalized: there is a supermajority of ratified accepted blocks that commits to it.
		ts.AssertNodeState(ts.Nodes(),
			testsuite.WithSnapshotImported(true),
			testsuite.WithProtocolParameters(ts.ProtocolParameters),
			testsuite.WithLatestCommitmentSlotIndex(9),
			testsuite.WithLatestStateMutationSlot(0),
			testsuite.WithLatestFinalizedSlot(1),
			testsuite.WithChainID(iotago.NewEmptyCommitment().MustID()),
			testsuite.WithSybilProtectionCommittee(expectedCommittee),
			testsuite.WithSybilProtectionOnlineCommittee(expectedCommittee),
			testsuite.WithActiveRootBlocks(ts.Blocks("3.1", "4.2", "5.1")),
			testsuite.WithStorageRootBlocks(ts.Blocks("Genesis", "1.1", "1.1*", "2.2", "2.2*", "3.1", "4.2", "5.1", "6.2", "7.1", "8.2", "9.1", "10.2")),
		)
		require.Equal(t, node1.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment(), node2.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment())

		// Slot 13
		{
			slot9Commitment := lo.PanicOnErr(node1.Protocol.MainEngineInstance().Storage.Commitments().Load(9)).Commitment()
			ts.IssueBlockAtSlot("13.1", 13, slot9Commitment, node1, ts.BlockID("12.2"))

			ts.AssertBlocksExist(ts.Blocks("13.1"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("12.2"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheAccepted(ts.Blocks("13.1"), false, ts.Nodes()...)

			ts.AssertBlocksInCacheRatifiedAccepted(ts.Blocks("10.2"), true, ts.Nodes()...)
			ts.AssertBlocksInCacheConfirmed(ts.Blocks("10.2"), true, ts.Nodes()...)
		}

		// Verify nodes' states:
		// - Slot 10 should be committed as the MinCommittableSlotAge is 1, and we accepted a block at slot 12.
		// - 10.1 is ratified accepted and commits to slot 5 -> slot 5 should be evicted.
		// - rootblocks are evicted until slot 2 as RootBlocksEvictionDelay is 3.
		// - slot 5 is finalized: there is a supermajority of ratified accepted blocks that commits to it.
		ts.AssertNodeState(ts.Nodes(),
			testsuite.WithSnapshotImported(true),
			testsuite.WithProtocolParameters(ts.ProtocolParameters),
			testsuite.WithLatestCommitmentSlotIndex(10),
			testsuite.WithLatestStateMutationSlot(0),
			testsuite.WithLatestFinalizedSlot(5),
			testsuite.WithChainID(iotago.NewEmptyCommitment().MustID()),
			testsuite.WithSybilProtectionCommittee(expectedCommittee),
			testsuite.WithSybilProtectionOnlineCommittee(expectedCommittee),
			testsuite.WithActiveRootBlocks(ts.Blocks("3.1", "4.2", "5.1")),
			testsuite.WithStorageRootBlocks(ts.Blocks("Genesis", "1.1", "1.1*", "2.2", "2.2*", "3.1", "4.2", "5.1", "6.2", "7.1", "8.2", "9.1", "10.2", "11.1")),
		)
		require.Equal(t, node1.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment(), node2.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment())
	}

	// Verify that node1 and node2 pruned until their respective slots.
	// TODO:

	// Shutdown node1 and restart it from disk. Verify state.
	// TODO:

	// Create snapshot.
	snapshotPath := ts.Directory.Path(fmt.Sprintf("%d_snapshot", time.Now().Unix()))
	require.NoError(t, node1.Protocol.MainEngineInstance().WriteSnapshot(snapshotPath))

	// Load node3 from created snapshot and verify state.
	{
		node3 := ts.AddNode("node3")
		node3.Initialize(
			protocol.WithSnapshotPath(snapshotPath),
			protocol.WithBaseDirectory(ts.Directory.PathWithCreate(node3.Name)),
			protocol.WithSybilProtectionProvider(
				poa.NewProvider(ts.Validators()),
			),
			protocol.WithNotarizationProvider(
				slotnotarization.NewProvider(slotnotarization.WithMinCommittableSlotAge(1)),
			),
		)
		ts.Wait()

		latestCommitment := lo.PanicOnErr(node1.Protocol.MainEngineInstance().Storage.Commitments().Load(10)).Commitment()
		// Verify node3 state:
		// - Commitment at slot 10 should be the latest commitment.
		// - 10-3 (RootBlocksEvictionDelay) = 7 -> rootblocks from slot 8 until 10 (count of 3).
		// - ChainID is defined by the earliest commitment of the rootblocks -> block 8.2 commits to slot 1.
		// - slot 5 is finalized as per snapshot.
		ts.AssertNodeState(ts.Nodes("node3"),
			testsuite.WithSnapshotImported(true),
			testsuite.WithProtocolParameters(ts.ProtocolParameters),
			testsuite.WithLatestCommitmentSlotIndex(10),
			testsuite.WithLatestCommitment(latestCommitment),
			testsuite.WithLatestStateMutationSlot(0),
			testsuite.WithLatestFinalizedSlot(5),
			// TODO: depends on rootblocks testsuite.WithChainID(iotago.NewEmptyCommitment().MustID()),
			testsuite.WithSybilProtectionCommittee(expectedCommittee),
			testsuite.WithSybilProtectionOnlineCommittee(expectedCommittee),
			testsuite.WithActiveRootBlocks(ts.Blocks("8.2", "9.1", "10.2")),
			testsuite.WithStorageRootBlocks(ts.Blocks("8.2", "9.1", "10.2")),
		)
		require.Equal(t, node1.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment(), node2.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Commitment())
	}
}
