package testsuite

import (
	"fmt"
	"math"
	"testing"

	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	"github.com/iotaledger/iota-core/pkg/testsuite/snapshotcreator"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/builder"
	"github.com/iotaledger/iota.go/v4/tpkg"
	"github.com/sajari/regression"
)

// Test_Regression runs benchmarks for many block types and find the best fit regression model.
func Test_Regression(t *testing.T) {
	r := new(regression.Regression)
	r.SetObserved("ns/op")
	r.SetVar(0, "Input")
	r.SetVar(1, "ContextInput")
	r.SetVar(2, "Output")
	r.SetVar(3, "NativeToken")
	r.SetVar(4, "Staking")
	r.SetVar(5, "BlockIssuer")
	r.SetVar(6, "Allotment")
	r.SetVar(7, "SignatureEd25519")

	r.Train(
		// one basic output as input, one basic output
		regression.DataPoint(basicInBasicOut(t, 1, 1, false)),
		// one basic outputs as input, multiple basic outputs
		regression.DataPoint(basicInBasicOut(t, 1, 20, false)),
		regression.DataPoint(basicInBasicOut(t, 1, iotago.MaxOutputsCount, false)),
		// multiple basic outputs as input, one basic output
		regression.DataPoint(basicInBasicOut(t, 20, 1, false)),
		regression.DataPoint(basicInBasicOut(t, iotago.MaxInputsCount, 1, false)),
		// multiple basic outputs as input, each with difference signature unlocks, one basic output
		regression.DataPoint(basicInBasicOut(t, 20, 1, true)),
		regression.DataPoint(basicInBasicOut(t, iotago.MaxInputsCount, 1, true)),
		// one basic output as input, one basic output and allotments
		regression.DataPoint(allotments(t, 1)),
		regression.DataPoint(allotments(t, 20)),
		regression.DataPoint(allotments(t, iotago.MaxAllotmentCount)),
		// one basic output as input, account outputs
		regression.DataPoint(basicInAccountOut(t, 1, false)),
		regression.DataPoint(basicInAccountOut(t, 20, false)),
		regression.DataPoint(basicInAccountOut(t, iotago.MaxOutputsCount, false)),
		// one basic output as input, account outputs with staking
		regression.DataPoint(basicInAccountOut(t, 1, true)),
		regression.DataPoint(basicInAccountOut(t, 20, true)),
		regression.DataPoint(basicInAccountOut(t, iotago.MaxOutputsCount, true)),
		// one account input, account outputs
		regression.DataPoint(accountInAccountOut(t, 1)),
		regression.DataPoint(accountInAccountOut(t, 20)),
		regression.DataPoint(accountInAccountOut(t, iotago.MaxOutputsCount-2)),
		// one basic output as input, native token outputs
		regression.DataPoint(basicInNativeOut(t, 1)),
		regression.DataPoint(basicInNativeOut(t, 20)),
		regression.DataPoint(basicInNativeOut(t, iotago.MaxOutputsCount-1)),
	)

	r.Run()
	coeffs := r.GetCoeffs()
	printCoefficients(coeffs)

	standardBlock := getStandardBlock(t)
	// calculate the workScoreParameters from the coefficients based on a desired Mana cost of 500,000
	// for a "standard block" creating an account from a basic output, and a ratio of 0.5 between the
	// workScore due to the dataByte factor and that due to rest of the factors.
	workScoreParams := workScoreParamsFromCoefficients(coeffs, standardBlock, 500_000, 0.5)
	ts := NewTestSuite(t, WithProtocolParametersOptions(iotago.WithWorkScoreOptions(
		workScoreParams.DataByte, workScoreParams.Block, workScoreParams.Input, workScoreParams.ContextInput,
		workScoreParams.Output, workScoreParams.NativeToken, workScoreParams.Staking, workScoreParams.BlockIssuer,
		workScoreParams.Allotment, workScoreParams.SignatureEd25519,
	)))
	standardBlock.API = ts.API
	// verify that the new workScore coefficients yield the desired cost for a standard block
	fmt.Printf("Standard block cost when RMC = 1: %d\n", lo.PanicOnErr(standardBlock.WorkScore()))
}

func getStandardBlock(t *testing.T) *iotago.Block {
	ts := NewTestSuite(t)
	node := ts.AddValidatorNode("node1")
	ts.AddDefaultWallet(node)
	ts.Run(true)
	tx := ts.DefaultWallet().CreateAccountFromInput(
		"tx",
		"Genesis:0",
		ts.DefaultWallet(),
		mock.WithBlockIssuerFeature(iotago.BlockIssuerKeys{tpkg.RandBlockIssuerKey()}, iotago.MaxSlotIndex),
	)
	genesisCommitment := iotago.NewEmptyCommitment(ts.API)
	genesisCommitment.ReferenceManaCost = ts.API.ProtocolParameters().CongestionControlParameters().MinReferenceManaCost
	block := ts.IssueBasicBlockWithOptions("block", ts.DefaultWallet(), tx, mock.WithSlotCommitment(genesisCommitment))

	return block.ProtocolBlock()
}

func basicInBasicOut(t *testing.T, numIn int, numOut int, signatures bool) (float64, []float64) {
	blockchan := make(chan *blocks.Block, 1)
	var block *iotago.Block

	// basic block with one input and one output
	fn := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			ts := NewTestSuite(t)
			node := ts.AddValidatorNode("node1")
			ts.AddDefaultWallet(node)
			ts.Run(true)
			var addressIndexes []uint32
			for i := 0; i < numIn; i++ {
				addressIndexes = append(addressIndexes, uint32(i))
			}
			// First, create 128 outputs
			var tx1 *iotago.SignedTransaction
			if signatures {
				tx1 = ts.DefaultWallet().CreateBasicOutputsAtAddressesFromInput(
					"tx1",
					addressIndexes,
					"Genesis:0",
				)
			} else {
				tx1 = ts.DefaultWallet().CreateBasicOutputsEquallyFromInput(
					"tx1",
					numIn,
					"Genesis:0",
				)
			}
			genesisCommitment := iotago.NewEmptyCommitment(ts.API)
			genesisCommitment.ReferenceManaCost = ts.API.ProtocolParameters().CongestionControlParameters().MinReferenceManaCost
			block1 := ts.IssueBasicBlockWithOptions("block1", ts.DefaultWallet(), tx1, mock.WithSlotCommitment(genesisCommitment))
			block = block1.ProtocolBlock()
			modelBlock := lo.PanicOnErr(model.BlockFromBlock(block))
			node.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				blockchan <- block
			})
			node.Protocol.IssueBlock(modelBlock)
			<-blockchan

			inputNames := make([]string, numIn)
			for i := 0; i < numIn; i++ {
				inputNames[i] = fmt.Sprintf("tx1:%d", i)
			}
			// Then, create a transaction with 128 inputs
			tx2 := ts.DefaultWallet().CreateBasicOutputsEquallyFromInputs(
				"tx2",
				inputNames,
				addressIndexes,
				numOut,
			)
			genesisCommitment.ReferenceManaCost = ts.API.ProtocolParameters().CongestionControlParameters().MinReferenceManaCost
			block2 := ts.IssueBasicBlockWithOptions("block2", ts.DefaultWallet(), tx2, mock.WithSlotCommitment(genesisCommitment))
			block = block2.ProtocolBlock()
			modelBlock = lo.PanicOnErr(model.BlockFromBlock(block))
			b.StartTimer()
			// time from issuance of the block to when it is scheduled
			node.Protocol.IssueBlock(modelBlock)
			<-blockchan
			b.StopTimer()
			ts.Shutdown()
		}
	}
	// get the ns/op of processing the block
	nsPerBlock := float64(testing.Benchmark(fn).NsPerOp())
	fmt.Printf("%d basic outputs as input, %d basic outputs", numIn, numOut)
	if signatures {
		fmt.Printf(", each with different signatures")
	}
	fmt.Printf(": %f ns/op\n", nsPerBlock)
	// get the regressors
	regressors := getBlockWorkScoreRegressors(block)
	printRegressors(regressors)

	return nsPerBlock, regressors
}

func basicInAccountOut(t *testing.T, numAccounts int, staking bool) (float64, []float64) {
	blockchan := make(chan *blocks.Block, 1)
	var block *iotago.Block

	// basic block with one input, one account output with staking and a remainder
	fn := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			ts := NewTestSuite(t)
			node := ts.AddValidatorNode("node1")
			ts.AddDefaultWallet(node)
			ts.Run(true)
			opts := []options.Option[builder.AccountOutputBuilder]{
				mock.WithBlockIssuerFeature(iotago.BlockIssuerKeys{tpkg.RandBlockIssuerKey()}, iotago.MaxSlotIndex),
			}
			if staking {
				opts = append(opts, mock.WithStakingFeature(10000, 421, 0, 10))
			}
			tx1 := ts.DefaultWallet().CreateAccountsFromInput(
				"tx1",
				"Genesis:0",
				numAccounts,
				opts...,
			)
			// default block issuer issues a block containing the transaction in slot 1.
			genesisCommitment := iotago.NewEmptyCommitment(ts.API)
			genesisCommitment.ReferenceManaCost = ts.API.ProtocolParameters().CongestionControlParameters().MinReferenceManaCost
			block1 := ts.IssueBasicBlockWithOptions("block1", ts.DefaultWallet(), tx1, mock.WithSlotCommitment(genesisCommitment))
			block = block1.ProtocolBlock()
			modelBlock := lo.PanicOnErr(model.BlockFromBlock(block))
			node.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				blockchan <- block
			})
			b.StartTimer()
			// time from issuance of the block to when it is scheduled
			node.Protocol.IssueBlock(modelBlock)
			<-blockchan
			b.StopTimer()
			ts.Shutdown()
		}
	}
	// get the ns/op of processing the block
	nsPerBlock := float64(testing.Benchmark(fn).NsPerOp())
	fmt.Printf("One basic output as input, %d account outputs", numAccounts)
	if staking {
		fmt.Printf(" with ")
	} else {
		fmt.Printf(" without ")
	}
	fmt.Printf("staking feature: %f ns/op\n", nsPerBlock)
	// get the regressors
	regressors := getBlockWorkScoreRegressors(block)
	printRegressors(regressors)

	return nsPerBlock, regressors
}

func accountInAccountOut(t *testing.T, numAccounts int) (float64, []float64) {
	if numAccounts > iotago.MaxOutputsCount-2 {
		panic("Can only create MaxOutputsCount - 2 account outputs because we two other outputs in genesis transaction to create inputs accounts)")
	}
	blockchan := make(chan *blocks.Block, 1)
	var block *iotago.Block

	// basic block with one input, one account output with staking and a remainder
	fn := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			ts := NewTestSuite(t)
			node := ts.AddValidatorNode("node1")
			ts.AddDefaultWallet(node)
			inputNames := []string{"Genesis:2"}
			for i := 0; i < numAccounts-1; i++ {
				ts.AddGenesisWallet(fmt.Sprintf("wallet%d", i), node)
				inputNames = append(inputNames, fmt.Sprintf("Genesis:%d", i+3))
			}
			ts.Run(true)
			tx1 := ts.DefaultWallet().TransitionAccounts(
				"tx1",
				inputNames,
				mock.WithBlockIssuerFeature(iotago.BlockIssuerKeys{tpkg.RandBlockIssuerKey()}, iotago.MaxSlotIndex),
			)
			// default block issuer issues a block containing the transaction in slot 1.
			genesisCommitment := iotago.NewEmptyCommitment(ts.API)
			genesisCommitment.ReferenceManaCost = ts.API.ProtocolParameters().CongestionControlParameters().MinReferenceManaCost
			block1 := ts.IssueBasicBlockWithOptions("block1", ts.DefaultWallet(), tx1, mock.WithSlotCommitment(genesisCommitment))
			block = block1.ProtocolBlock()
			modelBlock := lo.PanicOnErr(model.BlockFromBlock(block))
			node.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				blockchan <- block
			})
			b.StartTimer()
			// time from issuance of the block to when it is scheduled
			node.Protocol.IssueBlock(modelBlock)
			<-blockchan
			b.StopTimer()
			ts.Shutdown()
		}
	}
	// get the ns/op of processing the block
	nsPerBlock := float64(testing.Benchmark(fn).NsPerOp())
	fmt.Printf("One account input %d account outputs: %f ns/op\n", numAccounts, nsPerBlock)
	// get the regressors
	regressors := getBlockWorkScoreRegressors(block)
	printRegressors(regressors)

	return nsPerBlock, regressors
}

func basicInNativeOut(t *testing.T, nNative int) (float64, []float64) {
	if nNative > iotago.MaxOutputsCount-1 {
		panic("Can only create MaxOutputsCount - 1 native token outputs because we need an account output as well")
	}
	blockchan := make(chan *blocks.Block, 1)
	var block *iotago.Block
	fn := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			ts := NewTestSuite(t)
			ts.AddDefaultWallet(ts.AddValidatorNode("node1"))
			ts.Run(true)
			var addressIndexes []uint32
			for i := 0; i < nNative-1; i++ {
				addressIndexes = append(addressIndexes, uint32(i))
			}
			tx1 := ts.DefaultWallet().CreateFoundryAndNativeTokensFromInput(
				"tx1",
				"Genesis:0",
				"Genesis:2",
				addressIndexes...,
			)
			genesisCommitment := iotago.NewEmptyCommitment(ts.API)
			genesisCommitment.ReferenceManaCost = ts.API.ProtocolParameters().CongestionControlParameters().MinReferenceManaCost
			// issue a block with the transaction
			block1 := ts.IssueBasicBlockWithOptions("block1", ts.DefaultWallet(), tx1, mock.WithSlotCommitment(genesisCommitment))
			// get the protocol block
			block = block1.ProtocolBlock()
			// get the model block
			modelBlock := lo.PanicOnErr(model.BlockFromBlock(block))
			// hook the block scheduled event
			ts.DefaultWallet().Node.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				blockchan <- block
			})
			// start the timer
			b.StartTimer()
			// time from issuance of the block to when it is scheduled
			ts.DefaultWallet().Node.Protocol.IssueBlock(modelBlock)
			<-blockchan
			// stop the timer
			b.StopTimer()
			// shutdown the test suite
			ts.Shutdown()
		}
	}
	// get the ns/op of processing the block
	nsPerBlock := float64(testing.Benchmark(fn).NsPerOp())

	fmt.Printf("One input, %d native token outputs: %f ns/op\n", nNative, nsPerBlock)

	// get the regressors
	regressors := getBlockWorkScoreRegressors(block)
	printRegressors(regressors)
	return nsPerBlock, regressors
}

func nativeInNativeOut(t *testing.T) (float64, []float64) {
	blockchan := make(chan *blocks.Block, 1)
	var block *iotago.Block

	// basic block with one input and one output
	fn := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			ts := NewTestSuite(t)
			node := ts.AddValidatorNode("node1")
			ts.AddDefaultWallet(node)
			ts.Run(true)
			node.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				blockchan <- block
			})
			tx1 := ts.DefaultWallet().CreateFoundryAndNativeTokensFromInput(
				"tx1",
				"Genesis:0",
				"Genesis:2",
			)
			// default block issuer issues a block containing the transaction in slot 1.
			genesisCommitment := iotago.NewEmptyCommitment(ts.API)
			genesisCommitment.ReferenceManaCost = ts.API.ProtocolParameters().CongestionControlParameters().MinReferenceManaCost
			block1 := ts.IssueBasicBlockWithOptions("block1", ts.DefaultWallet(), tx1, mock.WithSlotCommitment(genesisCommitment))
			block = block1.ProtocolBlock()
			modelBlock := lo.PanicOnErr(model.BlockFromBlock(block))
			node.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				blockchan <- block
			})
			node.Protocol.IssueBlock(modelBlock)
			<-blockchan

			tx2 := ts.DefaultWallet().TransitionFoundry(
				"tx2",
				"tx1:0",
				"tx1:1",
			)
			genesisCommitment.ReferenceManaCost = ts.API.ProtocolParameters().CongestionControlParameters().MinReferenceManaCost
			block2 := ts.IssueBasicBlockWithOptions("block2", ts.DefaultWallet(), tx2, mock.WithSlotCommitment(genesisCommitment))
			block = block2.ProtocolBlock()
			modelBlock = lo.PanicOnErr(model.BlockFromBlock(block))
			b.StartTimer()
			// time from issuance of the block to when it is scheduled
			node.Protocol.IssueBlock(modelBlock)
			<-blockchan
			b.StopTimer()
			ts.Shutdown()
		}
	}
	// get the ns/op of processing the block
	nsPerBlock := float64(testing.Benchmark(fn).NsPerOp())
	fmt.Printf("One native token in one native token output: %f ns/op\n", nsPerBlock)
	// get the regressors
	regressors := getBlockWorkScoreRegressors(block)
	printRegressors(regressors)

	return nsPerBlock, regressors
}

func allotments(t *testing.T, numAllotments int) (float64, []float64) {
	blockchan := make(chan *blocks.Block, 1)
	var block *iotago.Block

	// basic block with one input and one output
	fn := func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			ts := NewTestSuite(t)
			// create genesis accounts to allot to.
			for i := 0; i < numAllotments; i++ {
				ts.AddGenesisAccount(snapshotcreator.AccountDetails{
					Address:              nil,
					Amount:               mock.MinIssuerAccountAmount(ts.API.ProtocolParameters()) * 10,
					Mana:                 0,
					IssuerKey:            tpkg.RandBlockIssuerKey(),
					ExpirySlot:           iotago.MaxSlotIndex,
					BlockIssuanceCredits: iotago.BlockIssuanceCredits(123),
				})
			}
			node := ts.AddValidatorNode("node1")
			ts.AddDefaultWallet(node)
			ts.Run(true)
			var accountIDs []iotago.AccountID
			for i := 0; i < numAllotments; i++ {
				accountOutput := ts.AccountOutput(fmt.Sprintf("Genesis:%d", i+1)).Output().(*iotago.AccountOutput)
				accountIDs = append(accountIDs, accountOutput.AccountID)
			}
			tx1 := ts.DefaultWallet().AllotManaFromInput(
				"tx1",
				"Genesis:0",
				accountIDs...,
			)
			// default block issuer issues a block containing the transaction in slot 1.
			genesisCommitment := iotago.NewEmptyCommitment(ts.API)
			genesisCommitment.ReferenceManaCost = ts.API.ProtocolParameters().CongestionControlParameters().MinReferenceManaCost
			block1 := ts.IssueBasicBlockWithOptions("block1", ts.DefaultWallet(), tx1, mock.WithSlotCommitment(genesisCommitment))
			block = block1.ProtocolBlock()
			modelBlock := lo.PanicOnErr(model.BlockFromBlock(block))
			node.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				blockchan <- block
			})
			b.StartTimer()
			// time from issuance of the block to when it is scheduled
			node.Protocol.IssueBlock(modelBlock)
			<-blockchan
			b.StopTimer()
			ts.Shutdown()
		}
	}
	// get the ns/op of processing the block
	nsPerBlock := float64(testing.Benchmark(fn).NsPerOp())
	fmt.Printf("One input, %d allotments: %f ns/op\n", numAllotments, nsPerBlock)
	// get the regressors
	regressors := getBlockWorkScoreRegressors(block)
	printRegressors(regressors)

	return nsPerBlock, regressors
}

func printRegressors(regressors []float64) {
	fmt.Printf("Input: %f\n", regressors[0])
	fmt.Printf("ContextInput: %f\n", regressors[1])
	fmt.Printf("Output: %f\n", regressors[2])
	fmt.Printf("NativeToken: %f\n", regressors[3])
	fmt.Printf("Staking: %f\n", regressors[4])
	fmt.Printf("BlockIssuer: %f\n", regressors[5])
	fmt.Printf("Allotment: %f\n", regressors[6])
	fmt.Printf("SignatureEd25519: %f\n", regressors[7])
}

func printCoefficients(coefficients []float64) {
	minCoeff := math.Abs(coefficients[0])
	for _, coeff := range coefficients {
		if math.Abs(coeff) < minCoeff {
			minCoeff = coeff
		}
	}
	normalisedCoeffs := make([]int, len(coefficients))
	for i, coeff := range coefficients {
		normalisedCoeffs[i] = int(coeff / minCoeff)
	}

	fmt.Println("Calculated coefficients from regression:")
	fmt.Printf("Block: %d\n", normalisedCoeffs[0])
	fmt.Printf("Input: %d\n", normalisedCoeffs[1])
	fmt.Printf("ContextInput: %d\n", normalisedCoeffs[2])
	fmt.Printf("Output: %d\n", normalisedCoeffs[3])
	fmt.Printf("NativeToken: %d\n", normalisedCoeffs[4])
	fmt.Printf("Staking: %d\n", normalisedCoeffs[5])
	fmt.Printf("BlockIssuer: %d\n", normalisedCoeffs[6])
	fmt.Printf("Allotment: %d\n", normalisedCoeffs[7])
	fmt.Printf("SignatureEd25519: %d\n", normalisedCoeffs[8])
}

func getBlockWorkScoreRegressors(block *iotago.Block) []float64 {
	regressors := make([]float64, 8)

	basicBlockBody, isBasic := block.Body.(*iotago.BasicBlockBody)
	if !isBasic {
		panic("block body is not a basic block body")
	}
	signedTx, isSignedTx := basicBlockBody.Payload.(*iotago.SignedTransaction)
	if !isSignedTx {
		panic("block payload is not a signed transaction")
	}
	// add one to the Input regressor for each input
	regressors[0] += float64(len(signedTx.Transaction.TransactionEssence.Inputs))
	// add one to the ContextInput regressor for each context input
	regressors[1] += float64(len(signedTx.Transaction.TransactionEssence.ContextInputs))
	for _, output := range signedTx.Transaction.Outputs {
		// add one to the Output regressor for each output
		regressors[2] += 1
		for _, feature := range output.FeatureSet() {
			switch feature.Type() {
			case iotago.FeatureNativeToken:
				// add one to the NativeToken regressor for each output with the native token feature
				regressors[3] += 1
			case iotago.FeatureStaking:
				// add one to the Staking regressor for each output with the staking feature
				regressors[4] += 1
			case iotago.FeatureBlockIssuer:
				// add one to the BlockIssuer regressor for each output with the block issuer feature
				regressors[5] += 1
			}
		}
	}
	// add one to Allotments regressor for each allotment
	regressors[6] += float64(len(signedTx.Transaction.TransactionEssence.Allotments))
	for _, unlock := range signedTx.Unlocks {
		if unlock.Type() == iotago.UnlockSignature {
			// add one to the SignatureEd25519 regressor for each unlock block
			regressors[7] += 1
		}
	}

	return regressors
}

// workScoreParamsFromCoefficients creates a WorkScoreParameters from the coefficients provided by the regression model.
// The parameters are scaled such that the the standardBlock has workScore of standardBlockMinCost. This represents the Mana cost
// of the "standard block" when the reference Mana cost (RMC) is at its minimum value of 1.
// The dataByteRatio is the ratio of the part of the WorkScore due to the DataBytes factor to that due to the rest of the factors.
func workScoreParamsFromCoefficients(coeffs []float64, standardBlock *iotago.Block, standardBlockMinCost iotago.Mana, dataByteRatio float64) iotago.WorkScoreParameters {
	standarBlockRegressors := getBlockWorkScoreRegressors(standardBlock)
	fmt.Printf("Standard block regressors: %+v\n", standarBlockRegressors)
	standardBlockWorkScore := coeffs[0]
	for i, regressor := range standarBlockRegressors {
		standardBlockWorkScore += regressor * coeffs[i+1]
	}
	scalingFactor := float64(standardBlockMinCost) / (standardBlockWorkScore * (1 + dataByteRatio))
	payloadSize := standardBlock.Body.(*iotago.BasicBlockBody).Payload.Size()
	dataByteFactor := (standardBlockWorkScore * (dataByteRatio * scalingFactor) / float64(payloadSize))

	return iotago.WorkScoreParameters{
		DataByte:         iotago.WorkScore(dataByteFactor),
		Block:            iotago.WorkScore(coeffs[0] * scalingFactor),
		Input:            iotago.WorkScore(coeffs[1] * scalingFactor),
		ContextInput:     iotago.WorkScore(coeffs[2] * scalingFactor),
		Output:           iotago.WorkScore(coeffs[3] * scalingFactor),
		NativeToken:      iotago.WorkScore(coeffs[4] * scalingFactor),
		Staking:          iotago.WorkScore(coeffs[5] * scalingFactor),
		BlockIssuer:      iotago.WorkScore(coeffs[6] * scalingFactor),
		Allotment:        iotago.WorkScore(coeffs[7] * scalingFactor),
		SignatureEd25519: iotago.WorkScore(coeffs[8] * scalingFactor),
	}
}
