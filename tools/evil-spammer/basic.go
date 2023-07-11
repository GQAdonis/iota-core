package main

import (
	"sync"
	"time"

	"github.com/iotaledger/iota-core/tools/evil-spammer/evilspammerpkg"
	"github.com/iotaledger/iota-core/tools/evilwallet"
	iotago "github.com/iotaledger/iota.go/v4"
)

type CustomSpamParams struct {
	ClientURLs            []string
	SpamTypes             []string
	Rates                 []int
	Durations             []time.Duration
	BlkToBeSent           []int
	TimeUnit              time.Duration
	DelayBetweenConflicts time.Duration
	NSpend                int
	Scenario              evilwallet.EvilBatch
	DeepSpam              bool
	EnableRateSetter      bool

	config *BasicConfig
}

func CustomSpam(params *CustomSpamParams) *BasicConfig {
	outputID := iotago.EmptyOutputID
	if params.config.LastFaucetUnspentOutputID != "" {
		outputID, _ = iotago.OutputIDFromHex(params.config.LastFaucetUnspentOutputID)
	}

	wallet := evilwallet.NewEvilWallet(evilwallet.WithClients(params.ClientURLs...), evilwallet.WithFaucetOutputID(outputID))
	wg := sync.WaitGroup{}

	fundsNeeded := false
	for _, st := range params.SpamTypes {
		if st != "blk" {
			fundsNeeded = true
		}
	}
	if fundsNeeded {
		err := wallet.RequestFreshBigFaucetWallet()
		if err != nil {
			panic(err)
		}
	}

	for i, sType := range params.SpamTypes {
		log.Infof("Start spamming with rate: %d, time unit: %s, and spamming type: %s.", params.Rates[i], params.TimeUnit.String(), sType)

		switch sType {
		case "blk":
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				s := SpamBlocks(wallet, params.Rates[i], params.TimeUnit, params.Durations[i], params.BlkToBeSent[i], params.EnableRateSetter)
				if s == nil {
					return
				}
				s.Spam()
			}(i)
		case "tx":
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				SpamTransaction(wallet, params.Rates[i], params.TimeUnit, params.Durations[i], params.DeepSpam, params.EnableRateSetter)
			}(i)
		case "ds":
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				SpamDoubleSpends(wallet, params.Rates[i], params.NSpend, params.TimeUnit, params.Durations[i], params.DelayBetweenConflicts, params.DeepSpam, params.EnableRateSetter)
			}(i)
		case "custom":
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				s := SpamNestedConflicts(wallet, params.Rates[i], params.TimeUnit, params.Durations[i], params.Scenario, params.DeepSpam, false, params.EnableRateSetter)
				if s == nil {
					return
				}
				s.Spam()
			}(i)
		case "commitments":
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
			}(i)

		default:
			log.Warn("Spamming type not recognized. Try one of following: tx, ds, blk")
		}
	}

	wg.Wait()
	log.Info("Basic spamming finished!")

	return &BasicConfig{
		LastFaucetUnspentOutputID: wallet.LastFaucetUnspentOutput().ToHex(),
	}
}

func SpamTransaction(wallet *evilwallet.EvilWallet, rate int, timeUnit, duration time.Duration, deepSpam, enableRateSetter bool) {
	if wallet.NumOfClient() < 1 {
		printer.NotEnoughClientsWarning(1)
	}

	scenarioOptions := []evilwallet.ScenarioOption{
		evilwallet.WithScenarioCustomConflicts(evilwallet.SingleTransactionBatch()),
	}
	if deepSpam {
		outWallet := wallet.NewWallet(evilwallet.Reuse)
		scenarioOptions = append(scenarioOptions,
			evilwallet.WithScenarioDeepSpamEnabled(),
			evilwallet.WithScenarioReuseOutputWallet(outWallet),
			evilwallet.WithScenarioInputWalletForDeepSpam(outWallet),
		)
	}
	scenarioTx := evilwallet.NewEvilScenario(scenarioOptions...)

	options := []evilspammerpkg.Options{
		evilspammerpkg.WithSpamRate(rate, timeUnit),
		evilspammerpkg.WithSpamDuration(duration),
		evilspammerpkg.WithRateSetter(enableRateSetter),
		evilspammerpkg.WithEvilWallet(wallet),
		evilspammerpkg.WithEvilScenario(scenarioTx),
	}
	spammer := evilspammerpkg.NewSpammer(options...)
	spammer.Spam()
}

func SpamDoubleSpends(wallet *evilwallet.EvilWallet, rate, nSpent int, timeUnit, duration, delayBetweenConflicts time.Duration, deepSpam, enableRateSetter bool) {
	log.Debugf("Setting up double spend spammer with rate: %d, time unit: %s, and duration: %s.", rate, timeUnit.String(), duration.String())
	if wallet.NumOfClient() < 2 {
		printer.NotEnoughClientsWarning(2)
	}

	scenarioOptions := []evilwallet.ScenarioOption{
		evilwallet.WithScenarioCustomConflicts(evilwallet.NSpendBatch(nSpent)),
	}
	if deepSpam {
		outWallet := wallet.NewWallet(evilwallet.Reuse)
		scenarioOptions = append(scenarioOptions,
			evilwallet.WithScenarioDeepSpamEnabled(),
			evilwallet.WithScenarioReuseOutputWallet(outWallet),
			evilwallet.WithScenarioInputWalletForDeepSpam(outWallet),
		)
	}
	scenarioDs := evilwallet.NewEvilScenario(scenarioOptions...)
	options := []evilspammerpkg.Options{
		evilspammerpkg.WithSpamRate(rate, timeUnit),
		evilspammerpkg.WithSpamDuration(duration),
		evilspammerpkg.WithEvilWallet(wallet),
		evilspammerpkg.WithRateSetter(enableRateSetter),
		evilspammerpkg.WithTimeDelayForDoubleSpend(delayBetweenConflicts),
		evilspammerpkg.WithEvilScenario(scenarioDs),
	}
	spammer := evilspammerpkg.NewSpammer(options...)
	spammer.Spam()
}

func SpamNestedConflicts(wallet *evilwallet.EvilWallet, rate int, timeUnit, duration time.Duration, conflictBatch evilwallet.EvilBatch, deepSpam, reuseOutputs, enableRateSetter bool) *evilspammerpkg.Spammer {
	scenarioOptions := []evilwallet.ScenarioOption{
		evilwallet.WithScenarioCustomConflicts(conflictBatch),
	}
	if deepSpam {
		outWallet := wallet.NewWallet(evilwallet.Reuse)
		scenarioOptions = append(scenarioOptions,
			evilwallet.WithScenarioDeepSpamEnabled(),
			evilwallet.WithScenarioReuseOutputWallet(outWallet),
			evilwallet.WithScenarioInputWalletForDeepSpam(outWallet),
		)
	} else if reuseOutputs {
		outWallet := wallet.NewWallet(evilwallet.Reuse)
		scenarioOptions = append(scenarioOptions, evilwallet.WithScenarioReuseOutputWallet(outWallet))
	}
	scenario := evilwallet.NewEvilScenario(scenarioOptions...)
	if scenario.NumOfClientsNeeded > wallet.NumOfClient() {
		printer.NotEnoughClientsWarning(scenario.NumOfClientsNeeded)
		return nil
	}

	options := []evilspammerpkg.Options{
		evilspammerpkg.WithSpamRate(rate, timeUnit),
		evilspammerpkg.WithSpamDuration(duration),
		evilspammerpkg.WithEvilWallet(wallet),
		evilspammerpkg.WithRateSetter(enableRateSetter),
		evilspammerpkg.WithEvilScenario(scenario),
	}

	return evilspammerpkg.NewSpammer(options...)
}

func SpamBlocks(wallet *evilwallet.EvilWallet, rate int, timeUnit, duration time.Duration, numBlkToSend int, enableRateSetter bool) *evilspammerpkg.Spammer {
	if wallet.NumOfClient() < 1 {
		printer.NotEnoughClientsWarning(1)
	}

	options := []evilspammerpkg.Options{
		evilspammerpkg.WithSpamRate(rate, timeUnit),
		evilspammerpkg.WithSpamDuration(duration),
		evilspammerpkg.WithBatchesSent(numBlkToSend),
		evilspammerpkg.WithRateSetter(enableRateSetter),
		evilspammerpkg.WithEvilWallet(wallet),
		evilspammerpkg.WithSpammingFunc(evilspammerpkg.DataSpammingFunction),
	}
	spammer := evilspammerpkg.NewSpammer(options...)
	return spammer
}
