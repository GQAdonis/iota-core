package utxoledger

import (
	"github.com/iotaledger/hive.go/kvstore"
	iotago "github.com/iotaledger/iota.go/v4"
)

type IterateOptions struct {
	readLockLedger bool
	maxResultCount int
}

type IterateOption func(*IterateOptions)

func ReadLockLedger(lockLedger bool) IterateOption {
	return func(args *IterateOptions) {
		args.readLockLedger = lockLedger
	}
}

func iterateOptions(optionalOptions []IterateOption) *IterateOptions {
	result := &IterateOptions{
		readLockLedger: true,
		maxResultCount: 0,
	}

	for _, optionalOption := range optionalOptions {
		optionalOption(result)
	}

	return result
}

func (m *Manager) ForEachOutput(consumer OutputConsumer, options ...IterateOption) error {
	opt := iterateOptions(options)

	if opt.readLockLedger {
		m.ReadLockLedger()
		defer m.ReadUnlockLedger()
	}

	var innerErr error
	var i int
	if err := m.store.Iterate([]byte{StoreKeyPrefixOutput}, func(key kvstore.Key, value kvstore.Value) bool {
		if (opt.maxResultCount > 0) && (i >= opt.maxResultCount) {
			return false
		}
		i++

		output := &Output{
			apiProvider: m.apiProvider,
		}
		if err := output.kvStorableLoad(m, key, value); err != nil {
			innerErr = err

			return false
		}

		return consumer(output)
	}); err != nil {
		return err
	}

	return innerErr
}

func (m *Manager) ForEachSpentOutput(consumer SpentConsumer, options ...IterateOption) error {
	opt := iterateOptions(options)

	if opt.readLockLedger {
		m.ReadLockLedger()
		defer m.ReadUnlockLedger()
	}

	key := []byte{StoreKeyPrefixOutputSpent}

	var innerErr error
	var i int
	if err := m.store.Iterate(key, func(key kvstore.Key, value kvstore.Value) bool {
		if (opt.maxResultCount > 0) && (i >= opt.maxResultCount) {
			return false
		}
		i++

		spent := &Spent{}
		if err := spent.kvStorableLoad(m, key, value); err != nil {
			innerErr = err

			return false
		}

		if err := m.loadOutputOfSpent(spent); err != nil {
			innerErr = err

			return false
		}

		return consumer(spent)
	}); err != nil {
		return err
	}

	return innerErr
}

func (m *Manager) SpentOutputs(options ...IterateOption) (Spents, error) {
	var spents Spents
	consumerFunc := func(spent *Spent) bool {
		spents = append(spents, spent)

		return true
	}

	if err := m.ForEachSpentOutput(consumerFunc, options...); err != nil {
		return nil, err
	}

	return spents, nil
}

func (m *Manager) ForEachUnspentOutputID(consumer OutputIDConsumer, options ...IterateOption) error {
	opt := iterateOptions(options)

	if opt.readLockLedger {
		m.ReadLockLedger()
		defer m.ReadUnlockLedger()
	}

	var innerErr error
	var i int
	if err := m.store.IterateKeys([]byte{StoreKeyPrefixOutputUnspent}, func(key kvstore.Key) bool {
		if (opt.maxResultCount > 0) && (i >= opt.maxResultCount) {
			return false
		}
		i++

		outputID, err := outputIDFromDatabaseKey(key)
		if err != nil {
			innerErr = err

			return false
		}

		return consumer(outputID)
	}); err != nil {
		return err
	}

	return innerErr
}

func (m *Manager) ForEachUnspentOutput(consumer OutputConsumer, options ...IterateOption) error {
	var innerErr error
	if err := m.ForEachUnspentOutputID(func(outputID iotago.OutputID) bool {
		outputKey := outputStorageKeyForOutputID(outputID)

		value, err := m.store.Get(outputKey)
		if err != nil {
			innerErr = err

			return false
		}

		output := &Output{
			apiProvider: m.apiProvider,
		}
		if err := output.kvStorableLoad(m, outputKey, value); err != nil {
			innerErr = err

			return false
		}

		return consumer(output)
	}, options...); err != nil {
		return err
	}

	return innerErr
}

func (m *Manager) UnspentOutputsIDs(options ...IterateOption) (iotago.OutputIDs, error) {
	var outputIDs iotago.OutputIDs
	consumerFunc := func(outputID iotago.OutputID) bool {
		outputIDs = append(outputIDs, outputID)

		return true
	}

	if err := m.ForEachUnspentOutputID(consumerFunc, options...); err != nil {
		return nil, err
	}

	return outputIDs, nil
}

func (m *Manager) UnspentOutputs(options ...IterateOption) (Outputs, error) {
	var outputs Outputs
	consumerFunc := func(output *Output) bool {
		outputs = append(outputs, output)

		return true
	}

	if err := m.ForEachUnspentOutput(consumerFunc, options...); err != nil {
		return nil, err
	}

	return outputs, nil
}

func (m *Manager) ComputeLedgerBalance(options ...IterateOption) (balance iotago.BaseToken, count int, err error) {
	balance = 0
	count = 0
	consumerFunc := func(output *Output) bool {
		count++
		balance += output.BaseTokenAmount()

		return true
	}

	if err := m.ForEachUnspentOutput(consumerFunc, options...); err != nil {
		return 0, 0, err
	}

	return balance, count, nil
}
