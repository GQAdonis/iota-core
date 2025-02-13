package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/core/safemath"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/log"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/testsuite"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"

	iotago "github.com/iotaledger/iota.go/v4"
)

// Supply for the test with faster slot duration and slots per epoch.
const SUPPLY = iotago.BaseToken(1_813_620_509_061_365)

func setupValidatorTestsuiteWithStake(t *testing.T, validator1Balance iotago.BaseToken, validator2Balance iotago.BaseToken, walletOpts ...options.Option[testsuite.WalletOptions]) *testsuite.TestSuite {
	var slotDuration uint8 = 5
	var slotsPerEpochExponent uint8 = 5
	var validationBlocksPerSlot uint8 = 5

	ts := testsuite.NewTestSuite(t,
		testsuite.WithProtocolParametersOptions(
			iotago.WithSupplyOptions(SUPPLY, 63, 1, 17, 32, 21, 70),
			iotago.WithStakingOptions(1, validationBlocksPerSlot, 1),
			// Pick larger values for ManaShareCoefficient and DecayBalancingConstant for more precision in the calculations.
			// Pick a small retention period so we can test rewards expiry.
			iotago.WithRewardsOptions(8, 11, 200, 5),
			// Pick Increase/Decrease threshold in accordance with sanity checks (necessary because we changed slot duration).
			iotago.WithCongestionControlOptions(1, 0, 0, 400_000, 300_000, 100_000, 1000, 100),
			iotago.WithTimeProviderOptions(
				0,
				testsuite.GenesisTimeWithOffsetBySlots(1000, slotDuration),
				slotDuration,
				slotsPerEpochExponent,
			),
			iotago.WithLivenessOptions(
				10,
				20,
				5,
				10,
				15,
			),
		),
	)

	// Add validator nodes to the network. This will add validator accounts to the snapshot.
	vnode1 := ts.AddValidatorNode("node1", append(
		[]options.Option[testsuite.WalletOptions]{
			testsuite.WithWalletAmount(validator1Balance),
		},
		walletOpts...,
	)...)
	vnode2 := ts.AddValidatorNode("node2", append(
		[]options.Option[testsuite.WalletOptions]{
			testsuite.WithWalletAmount(validator2Balance),
		},
		walletOpts...,
	)...)

	// Add a non-validator node to the network. This will not add any accounts to the snapshot.
	node3 := ts.AddNode("node3")
	// Add a default block issuer to the network. This will add another block issuer account to the snapshot.
	ts.AddDefaultWallet(vnode1)

	ts.Run(true)

	// For debugging set the log level appropriately.
	vnode1.Protocol.SetLogLevel(log.LevelInfo)
	vnode2.Protocol.SetLogLevel(log.LevelError)
	node3.Protocol.SetLogLevel(log.LevelError)

	ts.SetCurrentSlot(ts.API.ProtocolParameters().GenesisSlot() + 1)

	return ts
}

func setupValidatorTestsuite(t *testing.T, walletOpts ...options.Option[testsuite.WalletOptions]) *testsuite.TestSuite {
	return setupValidatorTestsuiteWithStake(t, 20_000_000, 25_000_000, walletOpts...)
}

type EpochPerformanceMap = map[iotago.EpochIndex]uint64
type ValidatorTest struct {
	ts *testsuite.TestSuite

	// How many validation blocks per slot the validator test nodes are supposed to issue.
	// Can be set to a different value than validationBlocksPerSlot to model under- or overissuance.
	issuancePerSlot uint8
	// The expected performance factor for each epoch.
	// The length of the map determines how many epochs the validators issue blocks.
	epochPerformanceFactors EpochPerformanceMap
}

func Test_Validator_PerfectIssuance(t *testing.T) {
	ts := setupValidatorTestsuite(t)
	defer ts.Shutdown()

	validationBlocksPerSlot := ts.API.ProtocolParameters().ValidationBlocksPerSlot()
	epochDurationSlots := uint64(ts.API.TimeProvider().EpochDurationSlots())

	test := ValidatorTest{
		ts:              ts,
		issuancePerSlot: validationBlocksPerSlot,
		epochPerformanceFactors: EpochPerformanceMap{
			// A validator cannot issue blocks in the genesis slot, so we deduct one slot worth of blocks.
			0: (uint64(validationBlocksPerSlot) * (epochDurationSlots - 1)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			1: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			2: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
		},
	}

	validatorTest(t, test)
}

func Test_Validator_PerfectIssuanceWithNonZeroFixedCost(t *testing.T) {
	ts := setupValidatorTestsuite(t, testsuite.WithWalletFixedCost(1000))
	defer ts.Shutdown()

	validationBlocksPerSlot := ts.API.ProtocolParameters().ValidationBlocksPerSlot()
	epochDurationSlots := uint64(ts.API.TimeProvider().EpochDurationSlots())

	test := ValidatorTest{
		ts:              ts,
		issuancePerSlot: validationBlocksPerSlot,
		epochPerformanceFactors: EpochPerformanceMap{
			// A validator cannot issue blocks in the genesis slot, so we deduct one slot worth of blocks.
			0: (uint64(validationBlocksPerSlot) * (epochDurationSlots - 1)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			1: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			2: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
		},
	}

	validatorTest(t, test)
}

func Test_Validator_PerfectIssuanceWithHugeStake(t *testing.T) {
	// This gives one validator almost the max supply as stake, which is unrealistic,
	// but is supposed to test if one validator with a huge stake causes an overflow in the rewards calculation.
	var validator1Balance iotago.BaseToken = 25_000_000
	var otherAccountBalance iotago.BaseToken = 31_700
	var genesisOutputBalance iotago.BaseToken = 14_100
	var validator2Balance iotago.BaseToken = SUPPLY - validator1Balance - otherAccountBalance - genesisOutputBalance

	ts := setupValidatorTestsuiteWithStake(t, iotago.BaseToken(validator1Balance), validator2Balance)
	defer ts.Shutdown()

	validationBlocksPerSlot := ts.API.ProtocolParameters().ValidationBlocksPerSlot()
	epochDurationSlots := uint64(ts.API.TimeProvider().EpochDurationSlots())

	test := ValidatorTest{
		ts:              ts,
		issuancePerSlot: validationBlocksPerSlot,
		epochPerformanceFactors: EpochPerformanceMap{
			// A validator cannot issue blocks in the genesis slot, so we deduct one slot worth of blocks.
			0: (uint64(validationBlocksPerSlot) * (epochDurationSlots - 1)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			1: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			2: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
		},
	}

	validatorTest(t, test)
}

func Test_Validator_PerfectIssuanceWithExpiredRewards(t *testing.T) {
	ts := setupValidatorTestsuite(t)
	defer ts.Shutdown()

	validationBlocksPerSlot := ts.API.ProtocolParameters().ValidationBlocksPerSlot()
	epochDurationSlots := uint64(ts.API.TimeProvider().EpochDurationSlots())

	test := ValidatorTest{
		ts:              ts,
		issuancePerSlot: validationBlocksPerSlot,
		// The retention period is 5, so the sum of rewards should be those from epochs 3 to 7 (= 5 epochs).
		epochPerformanceFactors: EpochPerformanceMap{
			// A validator cannot issue blocks in the genesis slot, so we deduct one slot worth of blocks.
			0: (uint64(validationBlocksPerSlot) * (epochDurationSlots - 1)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			1: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			2: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			3: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			4: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			5: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			6: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			7: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
		},
	}

	validatorTest(t, test)
}

func Test_Validator_OverIssuance(t *testing.T) {
	ts := setupValidatorTestsuite(t)
	defer ts.Shutdown()

	test := ValidatorTest{
		ts: ts,
		// Issue one more block than supposed to.
		issuancePerSlot: ts.API.ProtocolParameters().ValidationBlocksPerSlot() + 1,
		epochPerformanceFactors: EpochPerformanceMap{
			// We expect 0 rewards for overissuance.
			// We model that in this test by setting performance factor to 0.
			0: 0,
			1: 0,
			2: 0,
		},
	}

	validatorTest(t, test)
}

func Test_Validator_UnderIssuance(t *testing.T) {
	ts := setupValidatorTestsuite(t)
	defer ts.Shutdown()

	// Issue less than supposed to.
	validationBlocksPerSlot := ts.API.ProtocolParameters().ValidationBlocksPerSlot() - 2
	epochDurationSlots := uint64(ts.API.TimeProvider().EpochDurationSlots())

	test := ValidatorTest{
		ts:              ts,
		issuancePerSlot: validationBlocksPerSlot,
		epochPerformanceFactors: EpochPerformanceMap{
			// A validator cannot issue blocks in the genesis slot, so we deduct one slot worth of blocks.
			0: (uint64(validationBlocksPerSlot) * (epochDurationSlots - 1)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			1: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
			2: (uint64(validationBlocksPerSlot) * (epochDurationSlots)) >> uint64(ts.API.ProtocolParameters().SlotsPerEpochExponent()),
		},
	}

	validatorTest(t, test)
}

func Test_Validator_FixedCostExceedsRewards(t *testing.T) {
	ts := setupValidatorTestsuite(t, testsuite.WithWalletFixedCost(iotago.MaxMana))
	defer ts.Shutdown()

	validationBlocksPerSlot := ts.API.ProtocolParameters().ValidationBlocksPerSlot()

	test := ValidatorTest{
		ts:              ts,
		issuancePerSlot: validationBlocksPerSlot,
		epochPerformanceFactors: EpochPerformanceMap{
			0: 0,
			1: 0,
			2: 0,
		},
	}

	validatorTest(t, test)
}

func validatorTest(t *testing.T, test ValidatorTest) {
	ts := test.ts

	blockIssuanceInfo, err := ts.DefaultWallet().Client.BlockIssuance(context.Background())
	require.NoError(t, err)
	tip := blockIssuanceInfo.StrongParents[0]
	startEpoch := iotago.EpochIndex(0)
	endEpoch := iotago.EpochIndex(len(test.epochPerformanceFactors) - 1)
	fmt.Printf("ValidatorTest: startEpoch=%d, endEpoch=%d\n", startEpoch, endEpoch)

	// Calculate the period in which the validator will issue blocks.
	issuancePeriod := time.Duration(ts.API.ProtocolParameters().SlotDurationInSeconds()/test.issuancePerSlot) * time.Second
	// Validate until the last slot of the epoch is definitely committed.
	var endEpochSlot iotago.SlotIndex = ts.API.TimeProvider().EpochEnd(endEpoch) + ts.API.ProtocolParameters().MaxCommittableAge()
	// Needed to increase the block timestamp monotonically relative to the parent.
	subSlotBlockCounter := time.Duration(0)

	for slot := ts.CurrentSlot(); slot <= endEpochSlot; slot++ {
		ts.SetCurrentSlot(slot)

		slotStartTime := ts.API.TimeProvider().SlotStartTime(ts.CurrentSlot()).UTC()
		for subslotIdx := uint8(0); subslotIdx < test.issuancePerSlot; subslotIdx++ {
			for _, node := range ts.Validators() {
				blockName := fmt.Sprintf("block-%s-%d/%d", node.Name, ts.CurrentSlot(), subslotIdx)
				latestCommitment := node.Protocol.Engines.Main.Get().SyncManager.LatestCommitment().Commitment()

				issuingTime := slotStartTime.
					Add(issuancePeriod * time.Duration(subslotIdx)).
					Add(time.Duration(subSlotBlockCounter))

				validationBlock := lo.PanicOnErr(ts.IssueValidationBlockWithHeaderOptions(blockName, node,
					mock.WithSlotCommitment(latestCommitment),
					mock.WithIssuingTime(issuingTime),
					mock.WithStrongParents(tip),
				))

				tip = validationBlock.ID()
				subSlotBlockCounter += 1
			}
		}
	}

	ts.Wait(ts.Nodes()...)

	// Determine total stakes required for rewards calculation.
	var totalStake iotago.BaseToken = 0
	var totalValidatorStake iotago.BaseToken = 0
	lo.ForEach(ts.Validators(), func(n *mock.Node) {
		accountData, err := ts.DefaultWallet().Client.Validator(context.Background(), n.Validator.AccountData.Address)
		require.NoError(t, err)
		totalStake += accountData.PoolStake
		totalValidatorStake += accountData.ValidatorStake
	})

	// Determine the rewards the validators actually got.
	actualRewards := make(map[iotago.AccountID]iotago.Mana, len(ts.Validators()))
	claimingEpoch := ts.API.TimeProvider().EpochFromSlot(ts.CurrentSlot())
	retentionPeriod := iotago.EpochIndex(ts.API.ProtocolParameters().RewardsParameters().RetentionPeriod)

	for _, validatorAccount := range []string{"Genesis:1", "Genesis:2"} {
		output := ts.DefaultWallet().OutputData(validatorAccount)
		accountID := output.Output.(*iotago.AccountOutput).AccountID
		rewardresp, err := ts.DefaultWallet().Client.Rewards(context.Background(), output.ID)
		require.NoError(t, err)
		actualRewards[accountID] = rewardresp.Rewards
	}

	for accountID, actualReward := range actualRewards {
		lastRewardEpoch := iotago.EpochIndex(len(test.epochPerformanceFactors))
		rewards := make([]epochReward, 0, lastRewardEpoch)

		var firstRewardEpoch iotago.EpochIndex
		if retentionPeriod < lastRewardEpoch {
			firstRewardEpoch = lastRewardEpoch - retentionPeriod
		} else {
			firstRewardEpoch = 0
		}

		for epoch := firstRewardEpoch; epoch < lastRewardEpoch; epoch++ {
			epochPerformanceFactor := test.epochPerformanceFactors[epoch]
			epochReward := calculateEpochReward(ts, accountID, epoch, epochPerformanceFactor, totalStake, totalValidatorStake)
			rewards = append(rewards, epochReward)
		}

		expectedReward := calculateValidatorReward(ts, accountID, rewards, firstRewardEpoch, claimingEpoch)

		require.Equal(t, expectedReward, actualReward, "expected reward for account %s to be %d, was %d", accountID, expectedReward, actualReward)
	}
}

type epochReward struct {
	Mana         iotago.Mana
	ProfitMargin uint64
}

// Calculates the reward according to
// https://github.com/iotaledger/tips/blob/tip40/tips/TIP-0040/tip-0040.md#calculations-3.
//
// For testing purposes, assumes that the account's staking data is the same in the latest committed slot
// as in the epoch for which to calculate rewards.
func calculateEpochReward(ts *testsuite.TestSuite, accountID iotago.AccountID, epoch iotago.EpochIndex, epochPerformanceFactor uint64, totalStake iotago.BaseToken, totalValidatorStake iotago.BaseToken) epochReward {
	targetReward := lo.PanicOnErr(ts.API.ProtocolParameters().RewardsParameters().TargetReward(epoch, ts.API))
	accountAddress, ok := accountID.ToAddress().(*iotago.AccountAddress)
	if !ok {
		panic(fmt.Sprintf("accountID %s cannot be cast to account address", accountID))
	}
	stakingResp, err := ts.DefaultWallet().Client.Validator(context.Background(), accountAddress)
	require.NoError(ts.Testing, err)
	poolStake := stakingResp.PoolStake
	poolCoefficientExp := iotago.BaseToken(ts.API.ProtocolParameters().RewardsParameters().PoolCoefficientExponent)

	validationBlocksPerSlot := ts.API.ProtocolParameters().ValidationBlocksPerSlot()
	poolCoefficient := ((poolStake << poolCoefficientExp) / totalStake) + (stakingResp.ValidatorStake<<poolCoefficientExp)/totalValidatorStake

	scaledPoolReward := iotago.Mana(poolCoefficient) * targetReward * iotago.Mana(epochPerformanceFactor)
	poolReward := (scaledPoolReward / iotago.Mana(validationBlocksPerSlot)) >> (poolCoefficientExp + 1)

	profitMargin := (totalValidatorStake << iotago.BaseToken(ts.API.ProtocolParameters().RewardsParameters().ProfitMarginExponent)) / (totalValidatorStake + totalStake)

	return epochReward{Mana: poolReward, ProfitMargin: uint64(profitMargin)}
}

// Calculates a validator's reward according to
// https://github.com/iotaledger/tips/blob/tip40/tips/TIP-0040/tip-0040.md#calculations-4.
//
// For testing purposes, assumes that the account's staking data is the same in the latest committed slot
// as in the epoch for which to calculate rewards.
func calculateValidatorReward(ts *testsuite.TestSuite, accountID iotago.AccountID, epochRewards []epochReward, startEpoch iotago.EpochIndex, claimingEpoch iotago.EpochIndex) iotago.Mana {
	accountAddress, ok := accountID.ToAddress().(*iotago.AccountAddress)
	if !ok {
		panic(fmt.Sprintf("accountID %s cannot be cast to account address", accountID))
	}
	stakingResp, err := ts.DefaultWallet().Client.Validator(context.Background(), accountAddress)
	require.NoError(ts.Testing, err)
	profitMarginExponent := ts.API.ProtocolParameters().RewardsParameters().ProfitMarginExponent
	stakedAmount := stakingResp.ValidatorStake
	fixedCost := stakingResp.FixedCost
	poolStake := stakingResp.PoolStake
	rewardEpoch := startEpoch
	decayedRewards := iotago.Mana(0)

	for _, reward := range epochRewards {
		if reward.Mana >= fixedCost {
			rewardWithoutFixedCost := uint64(reward.Mana) - uint64(fixedCost)

			profitMarginComplement := (1 << profitMarginExponent) - reward.ProfitMargin
			profitMarginFactor := (reward.ProfitMargin * rewardWithoutFixedCost) >> profitMarginExponent

			intermediate := ((profitMarginComplement * rewardWithoutFixedCost) >> profitMarginExponent)
			residualValidatorFactor := lo.PanicOnErr(safemath.Safe64MulDiv(intermediate, uint64(stakedAmount), uint64(poolStake)))

			undecayedRewards := uint64(fixedCost) + profitMarginFactor + residualValidatorFactor

			decayedRewards += lo.PanicOnErr(ts.API.ManaDecayProvider().DecayManaByEpochs(iotago.Mana(undecayedRewards), rewardEpoch, claimingEpoch-1))
		} else {
			decayedRewards += 0
		}

		rewardEpoch++
	}

	return decayedRewards
}
