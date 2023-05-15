package slotnotarization

import (
	"encoding/binary"
	"io"

	"github.com/pkg/errors"

	"github.com/iotaledger/hive.go/ads"
	"github.com/iotaledger/hive.go/core/account"
	"github.com/iotaledger/hive.go/core/memstorage"
	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/runtime/module"
	"github.com/iotaledger/hive.go/runtime/syncutils"
	"github.com/iotaledger/hive.go/serializer/v2/marshalutil"
	"github.com/iotaledger/hive.go/serializer/v2/stream"
	"github.com/iotaledger/iota-core/pkg/traits"
	iotago "github.com/iotaledger/iota.go/v4"
)

const (
	PrefixAttestations byte = iota
	PrefixAttestationsLastCommittedSlot
	PrefixAttestationsWeight
)

type Attestations struct {
	persistentStorage    func(optRealm ...byte) kvstore.KVStore
	bucketedStorage      func(index iotago.SlotIndex) kvstore.KVStore
	weightsProviderFunc  func() *account.Accounts[iotago.AccountID, *iotago.AccountID]
	cachedAttestations   *memstorage.IndexedStorage[iotago.SlotIndex, iotago.AccountID, *shrinkingmap.ShrinkingMap[iotago.BlockID, *iotago.Attestation]]
	slotTimeProviderFunc func() *iotago.SlotTimeProvider
	mutex                *syncutils.DAGMutex[iotago.SlotIndex]

	traits.Committable
	module.Module
}

func NewAttestations(persistentStorage func(optRealm ...byte) kvstore.KVStore, bucketedStorage func(index iotago.SlotIndex) kvstore.KVStore, weightsProviderFunc func() *account.Accounts[iotago.AccountID, *iotago.AccountID], slotTimeProviderFunc func() *iotago.SlotTimeProvider) *Attestations {
	return &Attestations{
		Committable:          traits.NewCommittable(persistentStorage(), PrefixAttestationsLastCommittedSlot),
		persistentStorage:    persistentStorage,
		bucketedStorage:      bucketedStorage,
		weightsProviderFunc:  weightsProviderFunc,
		cachedAttestations:   memstorage.NewIndexedStorage[iotago.SlotIndex, iotago.AccountID, *shrinkingmap.ShrinkingMap[iotago.BlockID, *iotago.Attestation]](),
		slotTimeProviderFunc: slotTimeProviderFunc,
		mutex:                syncutils.NewDAGMutex[iotago.SlotIndex](),
	}
}

func (a *Attestations) Shutdown() {
	a.TriggerStopped()
}

func (a *Attestations) Add(attestation *iotago.Attestation) (added bool, err error) {
	slotIndex := a.slotTimeProviderFunc().IndexFromTime(attestation.IssuingTime)

	a.mutex.RLock(slotIndex)
	defer a.mutex.RUnlock(slotIndex)

	if slotIndex <= a.LastCommittedSlot() {
		return false, errors.Errorf("cannot add attestation: block is from past slot")
	}

	slotStorage := a.cachedAttestations.Get(slotIndex, true)
	issuerStorage, _ := slotStorage.GetOrCreate(attestation.IssuerID, func() *shrinkingmap.ShrinkingMap[iotago.BlockID, *iotago.Attestation] {
		return shrinkingmap.New[iotago.BlockID, *iotago.Attestation]()
	})

	blockID, err := attestation.BlockID(a.slotTimeProviderFunc())
	if err != nil {
		return false, err
	}

	return issuerStorage.Set(blockID, attestation), nil
}

func (a *Attestations) Delete(attestation *iotago.Attestation) (deleted bool, err error) {
	slotIndex := a.slotTimeProviderFunc().IndexFromTime(attestation.IssuingTime)

	a.mutex.RLock(slotIndex)
	defer a.mutex.RUnlock(slotIndex)

	if slotIndex <= a.LastCommittedSlot() {
		return false, errors.Errorf("cannot delete attestation from past slot %d", slotIndex)
	}

	slotStorage := a.cachedAttestations.Get(slotIndex, false)
	if slotStorage == nil {
		return false, nil
	}

	issuerStorage, exists := slotStorage.Get(attestation.IssuerID)
	if !exists {
		return false, nil
	}

	blockID, err := attestation.BlockID(a.slotTimeProviderFunc())
	if err != nil {
		return false, err
	}

	return issuerStorage.Delete(blockID), nil
}

func (a *Attestations) Commit(index iotago.SlotIndex) (attestations *ads.Map[iotago.AccountID, iotago.Attestation, *iotago.AccountID, *iotago.Attestation], weight int64, err error) {
	a.mutex.Lock(index)
	defer a.mutex.Unlock(index)

	if attestations, weight, err = a.commit(index); err != nil {
		return nil, 0, errors.Wrapf(err, "failed to commit attestations for slot %d", index)
	}

	if err = a.setWeight(index, weight); err != nil {
		return nil, 0, errors.Wrapf(err, "failed to commit attestations for slot %d", index)
	}

	a.SetLastCommittedSlot(index)

	if err = a.flush(index); err != nil {
		return nil, 0, errors.Wrapf(err, "failed to flush attestations for slot %d", index)
	}

	return
}

func (a *Attestations) Weight(index iotago.SlotIndex) (weight int64, err error) {
	a.mutex.RLock(index)
	defer a.mutex.RUnlock(index)

	if index > a.LastCommittedSlot() {
		return 0, errors.Errorf("cannot compute weight of attestations for slot %d: slot is not committed yet", index)
	}

	return a.weight(index)
}

func (a *Attestations) Get(index iotago.SlotIndex) (attestations *ads.Map[iotago.AccountID, iotago.Attestation, *iotago.AccountID, *iotago.Attestation], err error) {
	a.mutex.RLock(index)
	defer a.mutex.RUnlock(index)

	if index > a.LastCommittedSlot() {
		return nil, errors.Errorf("cannot retrieve attestations for slot %d: slot is not committed yet", index)
	}

	return a.attestations(index)
}

func (a *Attestations) Import(reader io.ReadSeeker) (err error) {
	slotIndex, err := stream.Read[uint64](reader)
	if err != nil {
		return errors.Wrap(err, "failed to read slot")
	}

	weight, err := stream.Read[int64](reader)
	if err != nil {
		return errors.Wrap(err, "failed to read weight for slot")
	}

	attestations, err := a.attestations(iotago.SlotIndex(slotIndex))
	if err != nil {
		return errors.Wrapf(err, "failed to import attestations for slot %d", slotIndex)
	}

	importedAttestation := new(iotago.Attestation)
	if err = stream.ReadCollection(reader, func(i int) (err error) {
		if err = stream.ReadSerializable(reader, importedAttestation); err != nil {
			return errors.Wrapf(err, "failed to read attestation %d", i)
		}

		attestations.Set(importedAttestation.IssuerID, importedAttestation)

		return
	}); err != nil {
		return errors.Wrapf(err, "failed to import attestations for slot %d", slotIndex)
	}

	if err = a.setWeight(iotago.SlotIndex(slotIndex), weight); err != nil {
		return errors.Wrapf(err, "failed to set attestations weight of slot %d", slotIndex)
	}

	a.SetLastCommittedSlot(iotago.SlotIndex(slotIndex))

	a.TriggerInitialized()

	return
}

func (a *Attestations) Export(writer io.WriteSeeker, targetSlot iotago.SlotIndex) (err error) {
	if err = stream.Write(writer, uint64(targetSlot)); err != nil {
		return errors.Wrap(err, "failed to write slot")
	}

	if weight, err := a.weight(targetSlot); targetSlot != 0 && err != nil {
		return errors.Wrap(err, "failed to obtain weight for slot")
	} else if err = stream.Write(writer, weight); err != nil {
		return errors.Wrap(err, "failed to write slot weight")
	}

	return stream.WriteCollection(writer, func() (elementsCount uint64, writeErr error) {
		attestations, writeErr := a.attestations(targetSlot)
		if writeErr != nil {
			return 0, errors.Wrapf(writeErr, "failed to export attestations for slot %d", targetSlot)
		}

		if streamErr := attestations.Stream(func(issuerID iotago.AccountID, attestation *iotago.Attestation) bool {
			if writeErr = stream.WriteSerializable(writer, attestation); writeErr != nil {
				writeErr = errors.Wrapf(writeErr, "failed to write attestation for issuer %s", issuerID)
			} else {
				elementsCount++
			}

			return writeErr == nil
		}); streamErr != nil {
			return 0, errors.Wrapf(streamErr, "failed to stream attestations of slot %d", targetSlot)
		}

		return
	})
}

func (a *Attestations) commit(index iotago.SlotIndex) (attestations *ads.Map[iotago.AccountID, iotago.Attestation, *iotago.AccountID, *iotago.Attestation], weight int64, err error) {
	if attestations, err = a.attestations(index); err != nil {
		return nil, 0, errors.Wrapf(err, "failed to get attestors for slot %d", index)
	}

	if cachedSlotStorage := a.cachedAttestations.Evict(index); cachedSlotStorage != nil {
		cachedSlotStorage.ForEach(func(id iotago.AccountID, attestationsOfID *shrinkingmap.ShrinkingMap[iotago.BlockID, *iotago.Attestation]) bool {
			if latestAttestation := latestAttestation(attestationsOfID); latestAttestation != nil {
				if attestorWeight, exists := a.weightsProviderFunc().Get(id); exists {
					attestations.Set(id, latestAttestation)

					weight += attestorWeight
				}
			}

			return true
		})
	}

	return
}

func (a *Attestations) flush(index iotago.SlotIndex) (err error) {
	if err = a.persistentStorage().Flush(); err != nil {
		return errors.Wrap(err, "failed to flush persistent storage")
	}

	if err = a.bucketedStorage(index).Flush(); err != nil {
		return errors.Wrapf(err, "failed to flush attestations for slot %d", index)
	}

	return
}

func (a *Attestations) attestations(index iotago.SlotIndex) (*ads.Map[iotago.AccountID, iotago.Attestation, *iotago.AccountID, *iotago.Attestation], error) {
	attestationsStorage, err := a.bucketedStorage(index).WithExtendedRealm([]byte{PrefixAttestations})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to access storage for attestors of slot %d", index)
	}

	return ads.NewMap[iotago.AccountID, iotago.Attestation](attestationsStorage), nil
}

func (a *Attestations) weight(index iotago.SlotIndex) (weight int64, err error) {
	value, err := a.bucketedStorage(index).Get([]byte{PrefixAttestationsWeight})
	if err != nil {
		if errors.Is(err, kvstore.ErrKeyNotFound) {
			return 0, nil
		}

		return 0, errors.Wrapf(err, "failed to retrieve weight of attestations for slot %d", index)
	}

	return int64(binary.LittleEndian.Uint64(value)), nil
}

func (a *Attestations) setWeight(index iotago.SlotIndex, weight int64) (err error) {
	weightBytes := make([]byte, marshalutil.Uint64Size)
	binary.LittleEndian.PutUint64(weightBytes, uint64(weight))

	if err = a.bucketedStorage(index).Set([]byte{PrefixAttestationsWeight}, weightBytes); err != nil {
		return errors.Wrapf(err, "failed to store weight of attestations for slot %d", index)
	}

	return
}

func latestAttestation(attestations *shrinkingmap.ShrinkingMap[iotago.BlockID, *iotago.Attestation]) (latestAttestation *iotago.Attestation) {
	attestations.ForEach(func(blockID iotago.BlockID, attestation *iotago.Attestation) bool {
		if attestation.Compare(latestAttestation) > 0 {
			latestAttestation = attestation
		}

		return true
	})

	return
}
