package protocol

import (
	"time"

	"github.com/iotaledger/hive.go/ads"
	"github.com/iotaledger/hive.go/core/eventticker"
	"github.com/iotaledger/hive.go/ds"
	"github.com/iotaledger/hive.go/ds/reactive"
	"github.com/iotaledger/hive.go/ds/types"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	"github.com/iotaledger/hive.go/runtime/workerpool"
	"github.com/iotaledger/iota-core/pkg/core/buffer"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/network"
	"github.com/iotaledger/iota-core/pkg/protocol/chainmanager"
	"github.com/iotaledger/iota-core/pkg/protocol/engine"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/merklehasher"
)

// BlockDispatcher is a component that is responsible for dispatching blocks to the correct engine instance or
// triggering a warp sync.
type BlockDispatcher struct {
	// protocol is the protocol instance that is using this BlockDispatcher instance.
	protocol *Protocol

	// dispatchWorkers is the worker pool that is used to dispatch blocks to the correct engine instance.
	dispatchWorkers *workerpool.WorkerPool

	// warpSyncWorkers is the worker pool that is used to process the WarpSync requests and responses.
	warpSyncWorkers *workerpool.WorkerPool

	// unsolidCommitmentBlocks is a buffer that stores blocks that have an unsolid slot commitment.
	unsolidCommitmentBlocks *buffer.UnsolidCommitmentBuffer[*types.Tuple[*model.Block, network.PeerID]]

	// pendingWarpSyncRequests is the set of pending requests that are waiting to be processed.
	pendingWarpSyncRequests *eventticker.EventTicker[iotago.SlotIndex, iotago.CommitmentID]

	// processedWarpSyncRequests is the set of processed requests.
	processedWarpSyncRequests ds.Set[iotago.CommitmentID]

	// shutdownEvent is a reactive event that is triggered when the BlockDispatcher instance is stopped.
	shutdownEvent reactive.Event
}

// NewBlockDispatcher creates a new BlockDispatcher instance.
func NewBlockDispatcher(protocol *Protocol) *BlockDispatcher {
	b := &BlockDispatcher{
		protocol:                  protocol,
		dispatchWorkers:           protocol.Workers.CreatePool("BlockDispatcher.Dispatch"),
		warpSyncWorkers:           protocol.Workers.CreatePool("BlockDispatcher.WarpSync", 1),
		unsolidCommitmentBlocks:   buffer.NewUnsolidCommitmentBuffer[*types.Tuple[*model.Block, network.PeerID]](20, 100),
		pendingWarpSyncRequests:   eventticker.New[iotago.SlotIndex, iotago.CommitmentID](eventticker.RetryInterval[iotago.SlotIndex, iotago.CommitmentID](WarpSyncRetryInterval)),
		processedWarpSyncRequests: ds.NewSet[iotago.CommitmentID](),
		shutdownEvent:             reactive.NewEvent(),
	}

	protocol.HookConstructed(b.initEngineMonitoring)
	protocol.HookInitialized(b.initNetworkConnection)
	protocol.HookStopped(b.shutdown)

	return b
}

// Dispatch dispatches the given block to the correct engine instance.
func (b *BlockDispatcher) Dispatch(block *model.Block, src network.PeerID) error {
	slotCommitment := b.protocol.ChainManager.LoadCommitmentOrRequestMissing(block.ProtocolBlock().SlotCommitmentID)
	if !slotCommitment.SolidEvent().WasTriggered() {
		if !b.unsolidCommitmentBlocks.Add(slotCommitment.ID(), types.NewTuple(block, src)) {
			return ierrors.Errorf("failed to add block %s to unsolid commitment buffer", block.ID())
		}

		return ierrors.Errorf("failed to dispatch block %s: slot commitment %s is not solid", block.ID(), slotCommitment.ID())
	}

	matchingEngineFound := false
	for _, engine := range []*engine.Engine{b.protocol.MainEngineInstance(), b.protocol.CandidateEngineInstance()} {
		if engine != nil && (engine.ChainID() == slotCommitment.Chain().ForkingPoint.ID() || engine.BlockRequester.HasTicker(block.ID())) {
			if !b.inWarpSyncRange(engine, block) {
				engine.ProcessBlockFromPeer(block, src)
			}

			matchingEngineFound = true
		}
	}

	if !matchingEngineFound {
		return ierrors.Errorf("failed to dispatch block %s: no matching engine found", block.ID())
	}

	return nil
}

// initEngineMonitoring initializes the automatic monitoring of the engine instances.
func (b *BlockDispatcher) initEngineMonitoring() {
	b.monitorLatestEngineCommitment(b.protocol.mainEngine)

	b.protocol.engineManager.OnEngineCreated(b.monitorLatestEngineCommitment)

	b.protocol.Events.ChainManager.CommitmentPublished.Hook(func(chainCommitment *chainmanager.ChainCommitment) {
		chainCommitment.SolidEvent().OnTrigger(func() {
			b.runTask(func() {
				b.injectUnsolidCommitmentBlocks(chainCommitment.Commitment().ID())
			}, b.dispatchWorkers)

			b.runTask(func() {
				b.warpSyncIfNecessary(b.targetEngine(chainCommitment), chainCommitment)
			}, b.warpSyncWorkers)
		})
	})

	b.protocol.Events.Engine.Notarization.LatestCommitmentUpdated.Hook(func(commitment *model.Commitment) {
		b.runTask(func() {
			b.injectUnsolidCommitmentBlocks(commitment.ID())
		}, b.dispatchWorkers)
	})

	b.protocol.Events.Engine.SlotGadget.SlotFinalized.Hook(b.evict)
}

// initNetworkConnection initializes the network connection of the BlockDispatcher instance.
func (b *BlockDispatcher) initNetworkConnection() {
	b.protocol.Events.Engine.BlockRequester.Tick.Hook(func(blockID iotago.BlockID) {
		b.runTask(func() {
			b.protocol.networkProtocol.RequestBlock(blockID)
		}, b.dispatchWorkers)
	})

	b.pendingWarpSyncRequests.Events.Tick.Hook(func(id iotago.CommitmentID) {
		b.runTask(func() {
			b.protocol.networkProtocol.SendWarpSyncRequest(id)
		}, b.dispatchWorkers)
	})

	b.protocol.Events.Network.BlockReceived.Hook(func(block *model.Block, src network.PeerID) {
		b.runTask(func() {
			b.protocol.HandleError(b.Dispatch(block, src))
		}, b.dispatchWorkers)
	})

	b.protocol.Events.Network.WarpSyncRequestReceived.Hook(func(commitmentID iotago.CommitmentID, src network.PeerID) {
		b.runTask(func() {
			b.protocol.HandleError(b.processWarpSyncRequest(commitmentID, src))
		}, b.warpSyncWorkers)
	})

	b.protocol.Events.Network.WarpSyncResponseReceived.Hook(func(commitmentID iotago.CommitmentID, blockIDs iotago.BlockIDs, merkleProof *merklehasher.Proof[iotago.Identifier], src network.PeerID) {
		b.runTask(func() {
			b.protocol.HandleError(b.processWarpSyncResponse(commitmentID, blockIDs, merkleProof, src))
		}, b.warpSyncWorkers)
	})
}

// processWarpSyncRequest processes a WarpSync request.
func (b *BlockDispatcher) processWarpSyncRequest(commitmentID iotago.CommitmentID, src network.PeerID) error {
	// TODO: check if the peer is allowed to request the warp sync

	committedSlot, err := b.protocol.MainEngineInstance().CommittedSlot(commitmentID.Index())
	if err != nil {
		return ierrors.Wrapf(err, "failed to get slot %d (not committed yet)", commitmentID.Index())
	}

	commitment, err := committedSlot.Commitment()
	if err != nil {
		return ierrors.Wrapf(err, "failed to get commitment from slot %d", commitmentID.Index())
	} else if commitment.ID() != commitmentID {
		return ierrors.Wrapf(err, "commitment ID mismatch: %s != %s", commitment.ID(), commitmentID)
	}

	blockIDs, err := committedSlot.BlockIDs()
	if err != nil {
		return ierrors.Wrapf(err, "failed to get block IDs from slot %d", commitmentID.Index())
	}

	roots, err := committedSlot.Roots()
	if err != nil {
		return ierrors.Wrapf(err, "failed to get roots from slot %d", commitmentID.Index())
	}

	b.protocol.networkProtocol.SendWarpSyncResponse(commitmentID, blockIDs, roots.TangleProof(), src)

	return nil
}

// processWarpSyncResponse processes a WarpSync response.
func (b *BlockDispatcher) processWarpSyncResponse(commitmentID iotago.CommitmentID, blockIDs iotago.BlockIDs, merkleProof *merklehasher.Proof[iotago.Identifier], _ network.PeerID) error {
	if b.processedWarpSyncRequests.Has(commitmentID) {
		return nil
	}

	chainCommitment, exists := b.protocol.ChainManager.Commitment(commitmentID)
	if !exists {
		return ierrors.Errorf("failed to get chain commitment for %s", commitmentID)
	}

	targetEngine := b.targetEngine(chainCommitment)
	if targetEngine == nil {
		return ierrors.Errorf("failed to get target engine for %s", commitmentID)
	}

	acceptedBlocks := ads.NewSet[iotago.BlockID](mapdb.NewMapDB(), iotago.BlockID.Bytes, iotago.SlotIdentifierFromBytes)
	for _, blockID := range blockIDs {
		_ = acceptedBlocks.Add(blockID) // a mapdb can newer return an error
	}

	if !iotago.VerifyProof(merkleProof, iotago.Identifier(acceptedBlocks.Root()), chainCommitment.Commitment().RootsID()) {
		return ierrors.Errorf("failed to verify merkle proof for %s", commitmentID)
	}

	b.pendingWarpSyncRequests.StopTicker(commitmentID)

	b.processedWarpSyncRequests.Add(commitmentID)

	targetEngine.Events.BlockDAG.BlockSolid.Hook(func(block *blocks.Block) {
		block.ID()
	})

	for _, blockID := range blockIDs {
		targetEngine.BlockDAG.GetOrRequestBlock(blockID)
	}

	return nil
}

// inWarpSyncRange returns whether the given block should be processed by a warp sync process.
//
// This is the case if the block is more than a warp sync threshold ahead of the latest commitment while also committing
// to a new slot that can be warp synced.
func (b *BlockDispatcher) inWarpSyncRange(engine *engine.Engine, block *model.Block) bool {
	if engine.BlockRequester.HasTicker(block.ID()) {
		return false
	}

	slotCommitmentID := block.ProtocolBlock().SlotCommitmentID
	latestCommitmentIndex := engine.Storage.Settings().LatestCommitment().Index()
	maxCommittableAge := engine.APIForSlot(slotCommitmentID.Index()).ProtocolParameters().MaxCommittableAge()

	return block.ID().Index() > latestCommitmentIndex+maxCommittableAge
}

// warpSyncIfNecessary checks if a warp sync is necessary and starts the process if that is the case.
func (b *BlockDispatcher) warpSyncIfNecessary(e *engine.Engine, chainCommitment *chainmanager.ChainCommitment) {
	if e == nil || chainCommitment == nil {
		return
	}

	chain := chainCommitment.Chain()
	maxCommittableAge := e.APIForSlot(chainCommitment.Commitment().Index()).ProtocolParameters().MaxCommittableAge()
	latestCommitmentIndex := e.Storage.Settings().LatestCommitment().Index()

	if chainCommitment.Commitment().Index() > latestCommitmentIndex+1 {
		for slotToWarpSync := latestCommitmentIndex + 1; slotToWarpSync <= latestCommitmentIndex+2*maxCommittableAge; slotToWarpSync++ {
			if commitmentToSync := chain.Commitment(slotToWarpSync); commitmentToSync != nil && !b.processedWarpSyncRequests.Has(commitmentToSync.ID()) {
				b.pendingWarpSyncRequests.StartTicker(commitmentToSync.ID())
			}
		}
	}
}

// injectUnsolidCommitmentBlocks injects the unsolid blocks for the given commitment ID into the correct engine
// instance.
func (b *BlockDispatcher) injectUnsolidCommitmentBlocks(id iotago.CommitmentID) {
	for _, tuple := range b.unsolidCommitmentBlocks.GetValues(id) {
		b.protocol.HandleError(b.Dispatch(tuple.A, tuple.B))
	}
}

// targetEngine returns the engine instance that should be used for the given commitment.
func (b *BlockDispatcher) targetEngine(commitment *chainmanager.ChainCommitment) *engine.Engine {
	if chain := commitment.Chain(); chain != nil {
		chainID := chain.ForkingPoint.Commitment().ID()

		if engine := b.protocol.MainEngineInstance(); engine.ChainID() == chainID {
			return engine
		}

		if engine := b.protocol.CandidateEngineInstance(); engine != nil && engine.ChainID() == chainID {
			return engine
		}
	}

	return nil
}

// monitorLatestEngineCommitment monitors the latest commitment of the given engine instance and triggers a warp sync if
// necessary.
func (b *BlockDispatcher) monitorLatestEngineCommitment(engineInstance *engine.Engine) {
	engineInstance.HookStopped(engineInstance.Events.Notarization.LatestCommitmentUpdated.Hook(func(commitment *model.Commitment) {
		if chainCommitment, exists := b.protocol.ChainManager.Commitment(commitment.ID()); exists {
			b.processedWarpSyncRequests.Delete(commitment.ID())

			b.warpSyncIfNecessary(engineInstance, chainCommitment)
		}
	}).Unhook)
}

// evict evicts all elements from the unsolid commitment blocks buffer and the pending warp sync requests that are older
// than the given index.
func (b *BlockDispatcher) evict(index iotago.SlotIndex) {
	b.pendingWarpSyncRequests.EvictUntil(index)
	b.unsolidCommitmentBlocks.EvictUntil(index)
}

// shutdown shuts down the BlockDispatcher instance.
func (b *BlockDispatcher) shutdown() {
	b.shutdownEvent.Compute(func(isShutdown bool) bool {
		if !isShutdown {
			b.pendingWarpSyncRequests.Shutdown()

			b.dispatchWorkers.Shutdown(true).ShutdownComplete.Wait()
			b.warpSyncWorkers.Shutdown(true).ShutdownComplete.Wait()
		}

		return true
	})
}

// runTask runs the given task on the given worker pool if the BlockDispatcher instance is not shutdown.
func (b *BlockDispatcher) runTask(task func(), pool *workerpool.WorkerPool) {
	b.shutdownEvent.Compute(func(isShutdown bool) bool {
		if !isShutdown {
			pool.Submit(task)
		}

		return isShutdown
	})
}


// WarpSyncRetryInterval is the interval in which a warp sync request is retried.
const WarpSyncRetryInterval = 1 * time.Minute
