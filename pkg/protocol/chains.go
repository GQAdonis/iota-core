package protocol

import (
	"fmt"

	"github.com/iotaledger/hive.go/ds/reactive"
	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/log"
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/iota-core/pkg/core/promise"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/protocol/engine"
	"github.com/iotaledger/iota-core/pkg/protocol/enginemanager"
	iotago "github.com/iotaledger/iota.go/v4"
)

type Chains struct {
	MainChain             reactive.Variable[*Chain]
	HeaviestChain         reactive.Variable[*Chain]
	HeaviestAttestedChain reactive.Variable[*Chain]
	HeaviestVerifiedChain reactive.Variable[*Chain]
	CommitmentCreated     *event.Event1[*Commitment]
	ChainCreated          *event.Event1[*Chain]

	protocol      *Protocol
	commitments   *shrinkingmap.ShrinkingMap[iotago.CommitmentID, *promise.Promise[*Commitment]]
	engineManager *enginemanager.EngineManager

	reactive.EvictionState[iotago.SlotIndex]
}

func newChains(protocol *Protocol) *Chains {
	c := &Chains{
		protocol:              protocol,
		EvictionState:         reactive.NewEvictionState[iotago.SlotIndex](),
		MainChain:             reactive.NewVariable[*Chain]().Init(NewChain(protocol.Logger)),
		HeaviestChain:         reactive.NewVariable[*Chain](),
		HeaviestAttestedChain: reactive.NewVariable[*Chain](),
		HeaviestVerifiedChain: reactive.NewVariable[*Chain](),
		commitments:           shrinkingmap.New[iotago.CommitmentID, *promise.Promise[*Commitment]](),
		CommitmentCreated:     event.New1[*Commitment](),
		ChainCreated:          event.New1[*Chain](),
		engineManager: enginemanager.New(
			protocol.Workers,
			func(err error) { protocol.LogError("engine error", "err", err) },
			protocol.options.BaseDirectory,
			3,
			protocol.options.StorageOptions,
			protocol.options.EngineOptions,
			protocol.options.FilterProvider,
			protocol.options.CommitmentFilterProvider,
			protocol.options.BlockDAGProvider,
			protocol.options.BookerProvider,
			protocol.options.ClockProvider,
			protocol.options.BlockGadgetProvider,
			protocol.options.SlotGadgetProvider,
			protocol.options.SybilProtectionProvider,
			protocol.options.NotarizationProvider,
			protocol.options.AttestationProvider,
			protocol.options.LedgerProvider,
			protocol.options.SchedulerProvider,
			protocol.options.TipManagerProvider,
			protocol.options.TipSelectionProvider,
			protocol.options.RetainerProvider,
			protocol.options.UpgradeOrchestratorProvider,
			protocol.options.SyncManagerProvider,
		),
	}

	c.ChainCreated.Hook(func(chain *Chain) {
		c.provideEngineIfRequested(chain)
		c.publishEngineCommitments(chain)
	})

	c.ChainCreated.Trigger(c.MainChain.Get())

	protocol.HookConstructed(func() {
		c.initMainChain()
		c.initWeightTracking()
		c.initChainSwitching()

		// TODO: trigger initialized
	})

	c.HeaviestChain.LogUpdates(c.protocol, log.LevelInfo, "Unchecked Heavier Chain", (*Chain).LogName)
	c.HeaviestAttestedChain.LogUpdates(c.protocol, log.LevelInfo, "Attested Heavier Chain", (*Chain).LogName)

	return c
}

func (c *Chains) PublishCommitment(commitment *model.Commitment) (commitmentMetadata *Commitment, published bool, err error) {
	request := c.requestCommitment(commitment.ID(), false)
	if request.WasRejected() {
		return nil, false, ierrors.Wrapf(request.Err(), "failed to request commitment %s", commitment.ID())
	}

	publishedCommitmentMetadata := NewCommitment(commitment, c.protocol.Logger)
	request.Resolve(publishedCommitmentMetadata).OnSuccess(func(resolvedMetadata *Commitment) {
		commitmentMetadata = resolvedMetadata
	})

	return commitmentMetadata, commitmentMetadata == publishedCommitmentMetadata, nil
}

func (c *Chains) Commitment(commitmentID iotago.CommitmentID, requestMissing ...bool) (commitment *Commitment, err error) {
	commitmentRequest, exists := c.commitments.Get(commitmentID)
	if !exists && lo.First(requestMissing) {
		if commitmentRequest = c.requestCommitment(commitmentID, true); commitmentRequest.WasRejected() {
			return nil, ierrors.Wrapf(commitmentRequest.Err(), "failed to request commitment %s", commitmentID)
		}
	}

	if commitmentRequest == nil || !commitmentRequest.WasCompleted() {
		return nil, ErrorCommitmentNotFound
	}

	if commitmentRequest.WasRejected() {
		return nil, commitmentRequest.Err()
	}

	return commitmentRequest.Result(), nil
}

func (c *Chains) MainEngineInstance() *engine.Engine {
	return c.MainChain.Get().Engine.Get()
}

func (c *Chains) initMainChain() {
	mainChain := c.MainChain.Get()
	mainChain.InstantiateEngine.Set(true)
	mainChain.Engine.OnUpdate(func(_, newEngine *engine.Engine) {
		c.protocol.Events.Engine.LinkTo(newEngine.Events)
	})
	mainChain.ForkingPoint.Get().IsRoot.Trigger()
}

func (c *Chains) setupCommitment(commitment *Commitment, slotEvictedEvent reactive.Event) {
	c.requestCommitment(commitment.PreviousCommitmentID(), true, lo.Void(commitment.Parent.Set)).OnError(func(err error) {
		c.protocol.LogDebug("failed to request previous commitment", "prevId", commitment.PreviousCommitmentID(), "error", err)
	})

	slotEvictedEvent.OnTrigger(func() {
		commitment.IsEvicted.Trigger()
	})

	commitment.SpawnedChain.OnUpdate(func(_, newChain *Chain) {
		if newChain != nil {
			c.ChainCreated.Trigger(newChain)
		}
	})

	c.CommitmentCreated.Trigger(commitment)
}

func (c *Chains) initWeightTracking() {
	trackHeaviestChain := func(chainVariable reactive.Variable[*Chain], getWeightVariable func(*Chain) reactive.Variable[uint64], candidate *Chain) (unsubscribe func()) {
		return getWeightVariable(candidate).OnUpdate(func(_, newChainWeight uint64) {
			if heaviestChain := c.HeaviestChain.Get(); heaviestChain != nil && newChainWeight < heaviestChain.VerifiedWeight.Get() {
				return
			}

			chainVariable.Compute(func(currentCandidate *Chain) *Chain {
				if currentCandidate == nil || currentCandidate.IsEvicted.WasTriggered() || newChainWeight > getWeightVariable(currentCandidate).Get() {
					return candidate
				}

				return currentCandidate
			})
		})
	}

	c.ChainCreated.Hook(func(chain *Chain) {
		trackHeaviestChain(c.HeaviestVerifiedChain, (*Chain).verifiedWeight, chain)
		trackHeaviestChain(c.HeaviestAttestedChain, (*Chain).attestedWeight, chain)
		trackHeaviestChain(c.HeaviestChain, (*Chain).claimedWeight, chain)
	})
}

func (c *Chains) initChainSwitching() {
	c.HeaviestChain.OnUpdate(func(prevCandidate, newCandidate *Chain) {
		if prevCandidate != nil {
			prevCandidate.RequestAttestations.Set(false)
		}

		if newCandidate != nil {
			newCandidate.RequestAttestations.Set(true)
		}
	})

	c.HeaviestAttestedChain.OnUpdate(func(prevCandidate, newCandidate *Chain) {
		if prevCandidate != nil {
			prevCandidate.InstantiateEngine.Set(false)
		}

		if newCandidate != nil {
			newCandidate.InstantiateEngine.Set(true)
		}
	})

	c.HeaviestVerifiedChain.OnUpdate(func(prevCandidate, newCandidate *Chain) {
		if prevCandidate != nil {
			//prevCandidate.InstantiateEngine.Set(false)
		}

		c.protocol.LogError("SWITCHED MAIN CHAIN")

		//go newCandidate.InstantiateEngine.Set(true)
	})
}

func (c *Chains) provideEngineIfRequested(chain *Chain) func() {
	return chain.InstantiateEngine.OnUpdate(func(_, instantiate bool) {
		if !instantiate {
			chain.SpawnedEngine.Set(nil)

			return
		}

		if currentEngine := chain.Engine.Get(); currentEngine == nil {
			mainEngine, err := c.engineManager.LoadActiveEngine(c.protocol.options.SnapshotPath)
			if err != nil {
				panic(fmt.Sprintf("could not load active engine: %s", err))
			}

			chain.SpawnedEngine.Set(mainEngine)

			c.protocol.Network.HookStopped(mainEngine.Shutdown)
		} else {
			forkingPoint := chain.ForkingPoint.Get()
			snapshotTargetIndex := forkingPoint.Index() - 1

			candidateEngineInstance, err := c.engineManager.ForkEngineAtSlot(snapshotTargetIndex)
			if err != nil {
				panic(ierrors.Wrap(err, "error creating new candidate engine"))

				return
			}

			chain.SpawnedEngine.Set(candidateEngineInstance)

			c.protocol.Network.HookStopped(candidateEngineInstance.Shutdown)
		}
	})
}

func (c *Chains) requestCommitment(commitmentID iotago.CommitmentID, requestFromPeers bool, optSuccessCallbacks ...func(metadata *Commitment)) (commitmentRequest *promise.Promise[*Commitment]) {
	slotEvicted := c.EvictionEvent(commitmentID.Index())
	if slotEvicted.WasTriggered() && c.LastEvictedSlot().Get() != 0 {
		forkingPoint := c.MainChain.Get().ForkingPoint.Get()

		if forkingPoint == nil || commitmentID != forkingPoint.ID() {
			return promise.New[*Commitment]().Reject(ErrorSlotEvicted)
		}

		for _, successCallback := range optSuccessCallbacks {
			successCallback(forkingPoint)
		}

		return promise.New[*Commitment]().Resolve(forkingPoint)
	}

	commitmentRequest, requestCreated := c.commitments.GetOrCreate(commitmentID, lo.NoVariadic(promise.New[*Commitment]))
	if requestCreated {
		if requestFromPeers {
			c.protocol.commitmentRequester.StartTicker(commitmentID)

			commitmentRequest.OnComplete(func() {
				c.protocol.commitmentRequester.StopTicker(commitmentID)
			})
		}

		commitmentRequest.OnSuccess(func(commitment *Commitment) {
			c.setupCommitment(commitment, slotEvicted)
		})

		slotEvicted.OnTrigger(func() { c.commitments.Delete(commitmentID) })
	}

	for _, successCallback := range optSuccessCallbacks {
		commitmentRequest.OnSuccess(successCallback)
	}

	return commitmentRequest
}

func (c *Chains) publishEngineCommitments(chain *Chain) {
	chain.SpawnedEngine.OnUpdateWithContext(func(_, engine *engine.Engine, withinContext func(subscriptionFactory func() (unsubscribe func()))) {
		if engine == nil {
			return
		}

		withinContext(func() (unsubscribe func()) {
			return engine.Ledger.HookInitialized(func() {
				withinContext(func() (unsubscribe func()) {
					var latestPublishedIndex iotago.SlotIndex

					publishCommitment := func(commitment *model.Commitment) (publishedCommitment *Commitment, published bool) {
						publishedCommitment, published, err := c.PublishCommitment(commitment)
						if err != nil {
							panic(err) // this can never happen, but we panic to get a stack trace if it ever does
						}

						publishedCommitment.promote(chain)
						publishedCommitment.AttestedWeight.Set(publishedCommitment.Weight.Get())
						publishedCommitment.IsAttested.Trigger()
						publishedCommitment.IsVerified.Trigger()

						latestPublishedIndex = commitment.Index()

						return publishedCommitment, published
					}

					if forkingPoint := chain.ForkingPoint.Get(); forkingPoint == nil {
						if rootCommitment, published := publishCommitment(engine.RootCommitment.Get()); published {
							chain.ForkingPoint.Set(rootCommitment)
						}
					} else {
						latestPublishedIndex = forkingPoint.Index() - 1
					}

					return engine.LatestCommitment.OnUpdate(func(_, latestModelCommitment *model.Commitment) {
						if latestModelCommitment == nil {
							// TODO: CHECK IF NECESSARY
							return
						}

						for latestPublishedIndex < latestModelCommitment.Index() {
							if commitmentToPublish, err := engine.Storage.Commitments().Load(latestPublishedIndex + 1); err != nil {
								panic(err) // this should never happen, but we panic to get a stack trace if it does
							} else {
								publishCommitment(commitmentToPublish)
							}
						}
					})
				})
			})

		})
	})
}
