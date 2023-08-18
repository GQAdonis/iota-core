package storage_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ds/types"
	"github.com/iotaledger/iota-core/pkg/storage"
	"github.com/iotaledger/iota-core/pkg/storage/database"
	iotago "github.com/iotaledger/iota.go/v4"
)

func TestStorage_Pruning(t *testing.T) {
	tf := NewTestFramework(t, "")
	defer tf.Shutdown()

	tf.GeneratePermanentData(100 * MB)
	tf.GeneratePrunableData(1, 100*MB)
	tf.GeneratePrunableData(2, 100*MB)
	for i := 0; i < 100; i++ {
		tf.GenerateSemiPermanentData(iotago.EpochIndex(i))
	}
}

func TestStorage_PruneByEpochIndex_SmallerDefault(t *testing.T) {
	tf := NewTestFramework(t, "", storage.WithPruningDelay(1))
	defer tf.Shutdown()

	totalEpochs := 10
	tf.GeneratePermanentData(10 * MB)
	for i := 1; i <= totalEpochs; i++ {
		tf.GeneratePrunableData(iotago.EpochIndex(i), 10*KB)
		tf.GenerateSemiPermanentData(iotago.EpochIndex(i))
	}

	tf.SetLatestFinalizedEpoch(9)

	// 7 > default pruning delay 1, should prune
	fmt.Println(tf.Instance.PruneByEpochIndex(7))
	tf.AssertPrunedUntil(
		types.NewTuple(6, true),
		types.NewTuple(0, true),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
	)

	// 8 > default pruning delay 1, should prune
	tf.Instance.PruneByEpochIndex(8)
	tf.AssertPrunedUntil(
		types.NewTuple(7, true),
		types.NewTuple(1, true),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
	)
}

func TestStorage_PruneByEpochIndex_BiggerDefault(t *testing.T) {
	tf := NewTestFramework(t, "", storage.WithPruningDelay(10))
	defer tf.Shutdown()

	totalEpochs := 14
	tf.GeneratePermanentData(10 * MB)
	for i := 1; i <= totalEpochs; i++ {
		tf.GeneratePrunableData(iotago.EpochIndex(i), 10*KB)
		tf.GenerateSemiPermanentData(iotago.EpochIndex(i))
	}

	tf.SetLatestFinalizedEpoch(13)

	// 7 < default pruning delay 10, should NOT prune
	err := tf.Instance.PruneByEpochIndex(7)
	require.ErrorContains(t, err, database.ErrNoPruningNeeded.Error())

	tf.AssertPrunedUntil(
		types.NewTuple(0, false),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
	)

	// 10 == default pruning delay 10, should NOT prune
	err = tf.Instance.PruneByEpochIndex(10)
	require.ErrorContains(t, err, database.ErrNoPruningNeeded.Error())

	tf.AssertPrunedUntil(
		types.NewTuple(0, true),
		types.NewTuple(0, true),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
	)

	// 12 > default pruning delay 10, should prune
	tf.Instance.PruneByEpochIndex(12)
	tf.AssertPrunedUntil(
		types.NewTuple(2, true),
		types.NewTuple(2, true),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
		types.NewTuple(0, false),
	)
}

func TestStorage_PruneBySize(t *testing.T) {
	tf := NewTestFramework(t, "",
		storage.WithPruningDelay(2),
		storage.WithPruningSizeEnable(true),
		storage.WithPruningSizeMaxTargetSizeBytes(10*MB))
	defer tf.Shutdown()

	totalEpochs := 14
	tf.GeneratePermanentData(5 * MB)
	for i := 1; i <= totalEpochs; i++ {
		tf.GeneratePrunableData(iotago.EpochIndex(i), 120*KB)
		tf.GenerateSemiPermanentData(iotago.EpochIndex(i))
	}

	tf.SetLatestFinalizedEpoch(13)

	// db size < target size 10 MB, should NOT prune
	err := tf.Instance.PruneBySize()
	require.ErrorContains(t, err, database.ErrNoPruningNeeded.Error())

	// prunable can't reached to pruned bytes size, should NOT prune
	err = tf.Instance.PruneBySize(4 * MB)
	require.ErrorContains(t, err, database.ErrNotEnoughHistory.Error())

	// prunable can reached to pruned bytes size, should prune
	err = tf.Instance.PruneBySize(7 * MB)
	require.NoError(t, err)
	require.LessOrEqual(t, tf.Instance.Size(), 7*MB)

	// execute goroutine that monitors the size of the database and prunes if necessary

	// special cases:
	//  - permanent is already bigger than target size
}

func TestStorage_RestoreFromDisk(t *testing.T) {
	tf := NewTestFramework(t, "", storage.WithPruningDelay(1))

	totalEpochs := 370
	tf.GeneratePermanentData(5 * MB)
	for i := 1; i <= totalEpochs; i++ {
		tf.GeneratePrunableData(iotago.EpochIndex(i), 1*B)
		tf.GenerateSemiPermanentData(iotago.EpochIndex(i))
	}

	tf.SetLatestFinalizedEpoch(366)
	tf.Instance.PruneByEpochIndex(366)
	tf.AssertPrunedUntil(
		types.NewTuple(365, true),
		types.NewTuple(359, true),
		types.NewTuple(1, true),
		types.NewTuple(1, true),
		types.NewTuple(1, true),
	)

	restoreDir := tf.BaseDir()
	tf.Shutdown()

	// restore from disk
	tf = NewTestFramework(t, restoreDir, storage.WithPruningDelay(1))
	tf.Instance.RestoreFromDisk()

	epoch, pruned := tf.Instance.LastPrunedEpoch()
	require.Equal(t, iotago.EpochIndex(365), epoch)
	require.True(t, pruned)
}
