package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/holiman/uint256"
	"github.com/urfave/cli"
)

type BlobUploadParams struct {
	Host             string
	To               common.Address
	PrivateKey       string
	File             string
	Value            string
	GasLimit         uint64
	GasPrice         string
	PriorityGasPrice string
	MaxFeePerBlobGas string
	ChainID          string
	Calldata         string
	BlobsPerTx       int
}

func MultipartUpload(params BlobUploadParams) (uint64, error) {
	value256, err := uint256.FromHex(params.Value)
	if err != nil {
		return 0, fmt.Errorf("invalid value param: %v", err)
	}

	data, err := os.ReadFile(params.File)
	if err != nil {
		return 0, fmt.Errorf("error reading blob file: %v", err)
	}

	chainId, _ := new(big.Int).SetString(params.ChainID, 0)

	ctx := context.Background()
	client, err := ethclient.DialContext(ctx, params.Host)
	if err != nil {
		log.Fatalf("Failed to connect to the Ethereum client: %v", err)
	}

	key, err := crypto.HexToECDSA(params.PrivateKey)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid private key", err)
	}

	var gasPrice256 *uint256.Int
	if params.GasPrice == "" {
		val, err := client.SuggestGasPrice(ctx)
		if err != nil {
			log.Fatalf("Error getting suggested gas price: %v", err)
		}
		var nok bool
		gasPrice256, nok = uint256.FromBig(val)
		if nok {
			log.Fatalf("gas price is too high! got %v", val.String())
		}
	} else {
		gasPrice256, err = DecodeUint256String(params.GasPrice)
		if err != nil {
			return 0, fmt.Errorf("%w: invalid gas price", err)
		}
	}

	priorityGasPrice256 := gasPrice256
	if params.PriorityGasPrice != "" {
		priorityGasPrice256, err = DecodeUint256String(params.PriorityGasPrice)
		if err != nil {
			return 0, fmt.Errorf("%w: invalid priority gas price", err)
		}
	}

	maxFeePerBlobGas256, err := DecodeUint256String(params.MaxFeePerBlobGas)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid max_fee_per_blob_gas", err)
	}

	calldataBytes, err := common.ParseHexOrString(params.Calldata)
	if err != nil {
		log.Fatalf("failed to parse calldata: %v", err)
	}

	var totalBlobGasUsed uint64

	blobChannel := make(chan FullBlobStruct)
	go EncodeMultipartBlob(blobChannel, data, params.BlobsPerTx)

	// First slot in which the transaction to upload blobs begins
	var initialSlot uint64

	for {
		select {
		case blobStruct, ok := <-blobChannel:
			if !ok {
				fmt.Printf("Operation costed %d BlobGas\n", totalBlobGasUsed)
				return initialSlot, nil
			}

			pendingNonce, err := client.PendingNonceAt(ctx, crypto.PubkeyToAddress(key.PublicKey))
			if err != nil {
				log.Fatalf("Error getting nonce: %v", err)
			}
			nonce := uint64(pendingNonce)

			tx := types.NewTx(&types.BlobTx{
				ChainID:    uint256.MustFromBig(chainId),
				Nonce:      nonce,
				GasTipCap:  priorityGasPrice256,
				GasFeeCap:  gasPrice256,
				Gas:        params.GasLimit,
				To:         params.To,
				Value:      value256,
				Data:       calldataBytes,
				BlobFeeCap: maxFeePerBlobGas256,
				BlobHashes: blobStruct.VersionedHashes,
				Sidecar:    &blobStruct.Sidecar,
			})
			signedTx, err := types.SignTx(tx, types.NewCancunSigner(chainId), key)
			if err != nil {
				log.Fatalf("failed to sign tx: %v", err)
			}

			rlpData, err := signedTx.MarshalBinary()
			if err != nil {
				log.Fatalf("failed to marshal tx: %v", err)
			}

			err = client.Client().CallContext(context.Background(), nil, "eth_sendRawTransaction", hexutil.Encode(rlpData))

			if err != nil {
				log.Fatalf("failed to send transaction: %v", err)
			} else {
				log.Printf("successfully sent transaction with %d blobs. Check https://blobscan.com/tx/%v", len(blobStruct.Sidecar.Blobs), signedTx.Hash())
				//for idxBlob, blob := range blobStruct.Sidecar.Blobs {
				//	log.Printf("Blob #%d: Size=%d", idxBlob, len(blob))
				//}
			}

			var receipt *types.Receipt

			for {
				receipt, err = client.TransactionReceipt(context.Background(), signedTx.Hash())
				if err == ethereum.NotFound {
					time.Sleep(1 * time.Second)
				} else if err != nil {
					if _, ok := err.(*json.UnmarshalTypeError); ok {
						// TODO: ignore other errors for now. Some clients are treating the blobGasUsed as big.Int rather than uint64
						break
					}
				} else {
					break
				}
			}

			log.Printf("Transaction included. nonce=%d, bloGasUsed=%d, blobGasPrice=%d. Check https://blobscan.com/block/%d", nonce, receipt.BlobGasUsed, receipt.BlobGasPrice, receipt.BlockNumber.Int64())
			if initialSlot == 0 {
				// Wait until the new block is indexed
				time.Sleep(24 * time.Second)
				initialSlot, err = GetSlotFromBlock(receipt.BlockNumber.Int64())
				if err != nil {
					return 0, err
				}
			}
			//log.Printf("Transaction included. nonce=%d hash=%v, block=%d, bloGasUsed=%d, blobGasPrice=%d", nonce, tx.Hash(), receipt.BlockNumber.Int64(), receipt.BlobGasUsed, receipt.BlobGasPrice)
			totalBlobGasUsed += receipt.BlobGasUsed
		}
	}
}

// TODO: block parameter
func MultiTxApp(cliCtx *cli.Context) error {
	startTime := time.Now()

	addr := cliCtx.String(TxRPCURLFlag.Name)
	to := common.HexToAddress(cliCtx.String(TxToFlag.Name))
	prv := cliCtx.String(TxPrivateKeyFlag.Name)
	file := cliCtx.String(TxBlobFileFlag.Name)
	// nonce := cliCtx.Int64(TxNonceFlag.Name)
	value := cliCtx.String(TxValueFlag.Name)
	gasLimit := cliCtx.Uint64(TxGasLimitFlag.Name)
	gasPrice := cliCtx.String(TxGasPriceFlag.Name)
	priorityGasPrice := cliCtx.String(TxPriorityGasPrice.Name)
	maxFeePerBlobGas := cliCtx.String(TxMaxFeePerBlobGas.Name)
	chainID := cliCtx.String(TxChainID.Name)
	calldata := cliCtx.String(TxCalldata.Name)
	blobsPerTx := cliCtx.Int(MultiTxBlobsPerTx.Name)

	params := BlobUploadParams{
		Host:             addr,
		To:               to,
		PrivateKey:       prv,
		File:             file,
		Value:            value,
		GasLimit:         gasLimit,
		GasPrice:         gasPrice,
		PriorityGasPrice: priorityGasPrice,
		MaxFeePerBlobGas: maxFeePerBlobGas,
		ChainID:          chainID,
		Calldata:         calldata,
		BlobsPerTx:       blobsPerTx,
	}

	_, err := MultipartUpload(params)
	if err != nil {
		fmt.Println(err)
		return err
	}

	elapsedTime := time.Since(startTime)
	fmt.Println("Operation took", elapsedTime)
	return nil
}
