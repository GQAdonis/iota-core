package slotstore

import (
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/hive.go/serializer/v2/stream"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
)

const (
	blockStorePrefix byte = iota
	transactionStorePrefix

	// api.TransactionState + api.TransactionFailureReason + iotago.SlotIndex.
	transactionRetainerDataLength = serializer.OneByte + serializer.OneByte + iotago.SlotIndexLength

	// api.BlockState + api.BlockFailureReason.
	blockRetainerDataLength = serializer.OneByte + serializer.OneByte
)

type BlockRetainerData struct {
	State         api.BlockState
	FailureReason api.BlockFailureReason
}

func (b *BlockRetainerData) Bytes() ([]byte, error) {
	byteBuffer := stream.NewByteBuffer(blockRetainerDataLength)

	if err := stream.Write(byteBuffer, b.State); err != nil {
		return nil, ierrors.Wrap(err, "failed to write block state")
	}
	if err := stream.Write(byteBuffer, b.FailureReason); err != nil {
		return nil, ierrors.Wrap(err, "failed to write block failure reason")
	}

	return byteBuffer.Bytes()
}

func blockRetainerDataFromBytes(bytes []byte) (*BlockRetainerData, int, error) {
	byteReader := stream.NewByteReader(bytes)

	var err error
	b := new(BlockRetainerData)

	if b.State, err = stream.Read[api.BlockState](byteReader); err != nil {
		return nil, 0, ierrors.Wrap(err, "failed to read block state")
	}

	if b.FailureReason, err = stream.Read[api.BlockFailureReason](byteReader); err != nil {
		return nil, 0, ierrors.Wrap(err, "failed to read block failure reason")
	}

	return b, byteReader.BytesRead(), nil
}

type TransactionRetainerData struct {
	State         api.TransactionState
	FailureReason api.TransactionFailureReason
	// needed for a finalization status evaluation
	ConfirmedAttachmentSlot iotago.SlotIndex
}

func (t *TransactionRetainerData) Bytes() ([]byte, error) {
	byteBuffer := stream.NewByteBuffer(transactionRetainerDataLength)

	if err := stream.Write(byteBuffer, t.State); err != nil {
		return nil, ierrors.Wrap(err, "failed to write transaction state")
	}

	if err := stream.Write(byteBuffer, t.FailureReason); err != nil {
		return nil, ierrors.Wrap(err, "failed to write transaction failure reason")
	}

	if err := stream.Write(byteBuffer, t.ConfirmedAttachmentSlot); err != nil {
		return nil, ierrors.Wrap(err, "failed to write confirmed attachment slot")
	}

	return byteBuffer.Bytes()
}

func transactionRetainerDataFromBytes(bytes []byte) (*TransactionRetainerData, int, error) {
	byteReader := stream.NewByteReader(bytes)

	var err error
	t := new(TransactionRetainerData)

	if t.State, err = stream.Read[api.TransactionState](byteReader); err != nil {
		return nil, 0, ierrors.Wrap(err, "failed to read transaction state")
	}

	if t.FailureReason, err = stream.Read[api.TransactionFailureReason](byteReader); err != nil {
		return nil, 0, ierrors.Wrap(err, "failed to read transaction failure reason")
	}

	if t.ConfirmedAttachmentSlot, err = stream.Read[iotago.SlotIndex](byteReader); err != nil {
		return nil, 0, ierrors.Wrap(err, "failed to read confirmed attachment slot")
	}

	return t, byteReader.BytesRead(), nil
}

type Retainer struct {
	slot       iotago.SlotIndex
	blockStore *kvstore.TypedStore[iotago.BlockID, *BlockRetainerData]
	// we store transaction metadata per blockID as in API requests we always request by blockID
	transactionStore *kvstore.TypedStore[iotago.TransactionID, *TransactionRetainerData]
}

func NewRetainer(slot iotago.SlotIndex, store kvstore.KVStore) (newRetainer *Retainer) {
	return &Retainer{
		slot: slot,
		blockStore: kvstore.NewTypedStore(lo.PanicOnErr(store.WithExtendedRealm(kvstore.Realm{blockStorePrefix})),
			iotago.BlockID.Bytes,
			iotago.BlockIDFromBytes,
			(*BlockRetainerData).Bytes,
			blockRetainerDataFromBytes,
		),
		transactionStore: kvstore.NewTypedStore(lo.PanicOnErr(store.WithExtendedRealm(kvstore.Realm{transactionStorePrefix})),
			iotago.TransactionID.Bytes,
			iotago.TransactionIDFromBytes,
			(*TransactionRetainerData).Bytes,
			transactionRetainerDataFromBytes,
		),
	}
}

func (r *Retainer) StoreBlockBooked(blockID iotago.BlockID) error {
	return r.blockStore.Set(blockID, &BlockRetainerData{
		State:         api.BlockStatePending,
		FailureReason: api.BlockFailureNone,
	})
}

func (r *Retainer) StoreBlockFailure(blockID iotago.BlockID, failureType api.BlockFailureReason) error {
	return r.blockStore.Set(blockID, &BlockRetainerData{
		State:         api.BlockStateFailed,
		FailureReason: failureType,
	})
}

func (r *Retainer) StoreBlockAccepted(blockID iotago.BlockID) error {
	data, err := r.blockStore.Get(blockID)
	if err != nil {
		return err
	}

	data.State = api.BlockStateAccepted
	data.FailureReason = api.BlockFailureNone

	return r.blockStore.Set(blockID, data)
}

func (r *Retainer) StoreBlockConfirmed(blockID iotago.BlockID) error {
	data, err := r.blockStore.Get(blockID)
	if err != nil {
		return err
	}

	data.State = api.BlockStateConfirmed
	data.FailureReason = api.BlockFailureNone

	return r.blockStore.Set(blockID, data)
}

func (r *Retainer) StoreBlockDropped(blockID iotago.BlockID) error {
	data, err := r.blockStore.Get(blockID)
	if err != nil {
		return err
	}

	data.State = api.BlockStateFailed
	data.FailureReason = api.BlockFailureDroppedDueToCongestion

	return r.blockStore.Set(blockID, data)
}

func (r *Retainer) GetBlock(blockID iotago.BlockID) (*BlockRetainerData, bool) {
	blockData, err := r.blockStore.Get(blockID)
	if err != nil {
		return nil, false
	}

	return blockData, true
}

func (r *Retainer) StoreTransactionData(transactionID iotago.TransactionID, data *TransactionRetainerData) error {
	return r.transactionStore.Set(transactionID, data)
}

func (r *Retainer) StoreTransactionNoFailure(transactionID iotago.TransactionID, status api.TransactionState) error {
	if status == api.TransactionStateFailed {
		return ierrors.Errorf("failed to retain transaction status, status cannot be failed, transactionID: %s", transactionID.String())
	}

	return r.transactionStore.Set(transactionID, &TransactionRetainerData{
		State:         status,
		FailureReason: api.TxFailureNone,
	})
}

func (r *Retainer) StoreTransactionFailure(transactionID iotago.TransactionID, failureType api.TransactionFailureReason) error {
	return r.transactionStore.Set(transactionID, &TransactionRetainerData{
		State:         api.TransactionStateFailed,
		FailureReason: failureType,
	})
}

func (r *Retainer) DeleteTransactionData(prevID iotago.TransactionID) error {
	return r.transactionStore.Delete(prevID)
}

func (r *Retainer) GetTransaction(txID iotago.TransactionID) (*TransactionRetainerData, bool) {
	txData, err := r.transactionStore.Get(txID)
	if err != nil {
		return nil, false
	}

	return txData, true
}
