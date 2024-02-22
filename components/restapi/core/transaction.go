package core

import (
	"github.com/labstack/echo/v4"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/inx-app/pkg/httpserver"
	"github.com/iotaledger/iota-core/pkg/model"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
)

func blockIDByTransactionID(c echo.Context) (iotago.BlockID, error) {
	txID, err := httpserver.ParseTransactionIDParam(c, api.ParameterTransactionID)
	if err != nil {
		return iotago.EmptyBlockID, ierrors.Wrapf(err, "failed to parse transaction ID %s", c.Param(api.ParameterTransactionID))
	}

	return blockIDFromTransactionID(txID)
}

func blockIDFromTransactionID(transactionID iotago.TransactionID) (iotago.BlockID, error) {
	// Get the first output of that transaction (using index 0)
	outputID := iotago.OutputIDFromTransactionIDAndIndex(transactionID, 0)

	output, spent, err := deps.Protocol.Engines.Main.Get().Ledger.OutputOrSpent(outputID)
	if err != nil {
		return iotago.EmptyBlockID, ierrors.Wrapf(echo.ErrInternalServerError, "failed to get output %s: %s", outputID.ToHex(), err)
	}

	if output != nil {
		return output.BlockID(), nil
	}

	return spent.BlockID(), nil
}

func blockByTransactionID(c echo.Context) (*model.Block, error) {
	blockID, err := blockIDByTransactionID(c)
	if err != nil {
		return nil, ierrors.Wrapf(echo.ErrBadRequest, "failed to get block ID by transaction ID: %s", err)
	}

	block, exists := deps.Protocol.Engines.Main.Get().Block(blockID)
	if !exists {
		return nil, ierrors.Wrapf(echo.ErrNotFound, "block not found: %s", blockID.ToHex())
	}

	return block, nil
}

func blockMetadataFromTransactionID(c echo.Context) (*api.BlockMetadataResponse, error) {
	blockID, err := blockIDByTransactionID(c)
	if err != nil {
		return nil, ierrors.Wrapf(echo.ErrBadRequest, "failed to get block ID by transaction ID: %s", err)
	}

	return blockMetadataByBlockID(blockID)
}

func transactionMetadataFromTransactionID(c echo.Context) (*api.TransactionMetadataResponse, error) {
	txID, err := httpserver.ParseTransactionIDParam(c, api.ParameterTransactionID)
	if err != nil {
		return nil, ierrors.Wrapf(err, "failed to parse transaction ID %s", c.Param(api.ParameterTransactionID))
	}

	// TODO: wire this up with the tx retainer
	//nolint:staticcheck // TODO: remove this once we have the transaction retainer
	transactionMetadata := &api.TransactionMetadataResponse{TransactionID: txID}
	//nolint:staticcheck // TODO: remove this once we have the transaction retainer
	if transactionMetadata == nil {
		return nil, ierrors.Wrapf(echo.ErrNotFound, "transaction not found")
	}

	return transactionMetadata, nil
}
