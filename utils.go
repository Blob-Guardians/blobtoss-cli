package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

type FullBlobStruct struct {
	Sidecar         types.BlobTxSidecar
	VersionedHashes []common.Hash
}

type BlobscanBlockResponse struct {
	Slot uint64 `json:"slot"`
}

// GetSlotFromBlock uses Blobscan API to retrieve slot number for a specific block
func GetSlotFromBlock(block int64) (uint64, error) {
	apiURL := fmt.Sprintf("https://api.blobscan.com/api/blocks/%d", block)

	resp, err := http.Get(apiURL)
	if err != nil {
		fmt.Println("Error making HTTP request:", err)
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println(apiURL, "returned non-OK status code:", resp.StatusCode)
		return 0, err
	}

	var responseObject BlobscanBlockResponse
	err = json.NewDecoder(resp.Body).Decode(&responseObject)
	if err != nil {
		fmt.Println("Error decoding JSON:", err)
		return 0, err
	}

	fmt.Println("Retrieved slot", responseObject.Slot, "for block", block)

	return responseObject.Slot, nil
}

func getTotalBlobs(data []byte) int {
	fileSize := len(data)
	totalBlobs := fileSize / 131072
	remainder := fileSize % 131072

	if remainder > 0 {
		totalBlobs += 1
	}
	fmt.Printf("File size is %d bytes and will be split into %d blobs of 128KB\n", fileSize, totalBlobs)
	return totalBlobs
}

// TODO: func copyMagicHeader()
// TODO: Pre-calculate total cost of sending file (single or multipart)
// encodeMultipartBlobs does encode `blobsPerTx` blobs in one transaction. Blobs will contain a magic header that allows
// identifying different pieces of the files. The magic header also contains the blob number and total of blobs.
func encodeBlobsWithMagicHeader(data []byte) []kzg4844.Blob {
	blobs := []kzg4844.Blob{{}}

	totalBlobs := getTotalBlobs(data)
	if totalBlobs > 255 {
		fmt.Println("More than 255 blobs is not supported in this version")
		return blobs
	}

	blobIndex := 0
	fieldIndex := 0
	seed := uint64(time.Now().UnixNano())
	magicHeader := generateMagicHeader(blobIndex, totalBlobs, seed)
	copy(blobs[blobIndex][fieldIndex*32:], magicHeader)
	// fmt.Printf("%d %d %x || %d\n", 0, fieldIndex, magicHeader, magicHeader)
	fieldIndex++

	for i := 0; i < len(data); i += 31 {
		if fieldIndex == params.BlobTxFieldElementsPerBlob {
			blobs = append(blobs, kzg4844.Blob{})
			blobIndex++
			fieldIndex = 0
			// fmt.Printf("---- BEGIN OF BLOB %d -----\n", blobIndex)

			magicHeader := generateMagicHeader(blobIndex, totalBlobs, seed)
			copy(blobs[blobIndex][fieldIndex*32:], magicHeader)
			// fmt.Printf("%d %d %x || %d\n", 0, fieldIndex, magicHeader, magicHeader)
			fieldIndex++
		}
		max := i + 31
		if max > len(data) {
			max = len(data)
		}
		// fmt.Println(i, fieldIndex, data[i:max])
		// fmt.Printf("%d %d %x || %d\n", i, fieldIndex, data[i:max], data[i:max])
		copy(blobs[blobIndex][fieldIndex*32+1:], data[i:max])
		fieldIndex++
	}

	return blobs
}

func encodeBlobs(data []byte) []kzg4844.Blob {
	blobs := []kzg4844.Blob{{}}
	blobIndex := 0
	fieldIndex := -1
	for i := 0; i < len(data); i += 31 {
		fieldIndex++
		if fieldIndex == params.BlobTxFieldElementsPerBlob {
			blobs = append(blobs, kzg4844.Blob{})
			blobIndex++
			fieldIndex = 0
		}
		max := i + 31
		if max > len(data) {
			max = len(data)
		}
		copy(blobs[blobIndex][fieldIndex*32+1:], data[i:max])
	}
	return blobs
}

// magicHeader is a 32 bytes array containing a string we use to identify files splitted in multiple blobs
// plus blobIndex and totalBlobs
func generateMagicHeader(blobIndex, totalBlobs int, seed uint64) []byte {

	randomBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(randomBytes, seed)
	magicHeader := make([]byte, 32)

	copy(magicHeader, []byte{66, 108, 111, 98, 115, 65, 114, 101, 67, 111, 109, 105, 110, 103, 46, 1, 46, byte(blobIndex), 46, byte(totalBlobs), 46, 46, 46, 46})
	copy(magicHeader[24:], randomBytes)

	//fmt.Printf("Magic header (len=%d): %v\n", len(magicHeader), magicHeader)
	return magicHeader
}

// EncodeMultipartBlob packs blob txs and sends them through the blobChannel,
// ready to be broadcasted.
// The main difference between EncodeBlobs and EncodeMultipartBlobs are...
// 1. Adds magic header
// 2. Sends them through channel instead of returning so that it can be right broadcasted
func EncodeMultipartBlob(blobChannel chan<- FullBlobStruct, data []byte, blobsPerTx int) {
	var (
		allBlobs        = encodeBlobsWithMagicHeader(data)
		blobs           []kzg4844.Blob
		commits         []kzg4844.Commitment
		proofs          []kzg4844.Proof
		versionedHashes []common.Hash
	)

	if blobsPerTx > 8 {
		fmt.Println("Max blobs per transaction is 6")
		close(blobChannel)
		return
	}

	uploadSeconds := len(allBlobs)/blobsPerTx*12 + 12
	fmt.Printf("Total blobs: %d. Approximate upload time: %d seconds at %d blobs per tx\n", len(allBlobs), uploadSeconds, blobsPerTx)

	for blobIndex, blob := range allBlobs {
		blobs = append(blobs, blob)
		commit, err := kzg4844.BlobToCommitment(blob)
		if err != nil {
			fmt.Println(err)
			return
		}
		commits = append(commits, commit)

		proof, err := kzg4844.ComputeBlobProof(blob, commit)
		if err != nil {
			fmt.Println(err)
			return
		}
		proofs = append(proofs, proof)
		versionedHashes = append(versionedHashes, kZGToVersionedHash(commit))

		if (blobIndex+1)%blobsPerTx == 0 {
			sidecar := types.BlobTxSidecar{
				Blobs:       blobs,
				Commitments: commits,
				Proofs:      proofs,
			}
			blobStruct := FullBlobStruct{Sidecar: sidecar, VersionedHashes: versionedHashes}
			blobChannel <- blobStruct

			// Reset
			blobs = []kzg4844.Blob{}
			commits = []kzg4844.Commitment{}
			proofs = []kzg4844.Proof{}
			versionedHashes = []common.Hash{}
		}
	}

	if len(blobs) > 0 {
		sidecar := types.BlobTxSidecar{
			Blobs:       blobs,
			Commitments: commits,
			Proofs:      proofs,
		}
		blobStruct := FullBlobStruct{Sidecar: sidecar, VersionedHashes: versionedHashes}
		blobChannel <- blobStruct
	}
	close(blobChannel)
}

func EncodeBlobs(data []byte) (*types.BlobTxSidecar, []common.Hash, error) {
	var (
		blobs           = encodeBlobs(data)
		commits         []kzg4844.Commitment
		proofs          []kzg4844.Proof
		versionedHashes []common.Hash
	)

	for _, blob := range blobs {
		commit, err := kzg4844.BlobToCommitment(blob)
		if err != nil {
			return nil, nil, err
		}
		commits = append(commits, commit)

		proof, err := kzg4844.ComputeBlobProof(blob, commit)
		if err != nil {
			return nil, nil, err
		}
		proofs = append(proofs, proof)

		versionedHashes = append(versionedHashes, kZGToVersionedHash(commit))
	}
	sidecar := types.BlobTxSidecar{
		Blobs:       blobs,
		Commitments: commits,
		Proofs:      proofs,
	}
	return &sidecar, versionedHashes, nil
}

var blobCommitmentVersionKZG uint8 = 0x01

// kZGToVersionedHash implements kzg_to_versioned_hash from EIP-4844
func kZGToVersionedHash(kzg kzg4844.Commitment) common.Hash {
	h := sha256.Sum256(kzg[:])
	h[0] = blobCommitmentVersionKZG

	return h
}

func DecodeMagicBlob(blob []byte) []byte {
	if len(blob) != params.BlobTxFieldElementsPerBlob*32 {
		fmt.Printf("len blob found: %d, expected: %d\n", len(blob), params.BlobTxFieldElementsPerBlob*32-33)
		panic("invalid blob encoding")
	}
	var data []byte

	// XXX: the following removes trailing 0s in each field element (see EncodeBlobs), which could be unexpected for certain blobs
	// And skips the magic header
	j := 33
	for i := 1; i < params.BlobTxFieldElementsPerBlob; i++ {
		data = append(data, blob[j:j+31]...)
		j += 32
	}

	i := len(data) - 1
	for ; i >= 0; i-- {
		if data[i] != 0x00 {
			break
		}
	}
	data = data[:i+1]
	return data
}

func DecodeBlob(blob []byte) []byte {
	if len(blob) != params.BlobTxFieldElementsPerBlob*32 {
		fmt.Printf("len blob found: %d, expected: %d\n", len(blob), params.BlobTxFieldElementsPerBlob*32)
		panic("invalid blob encoding")
	}
	var data []byte

	// XXX: the following removes trailing 0s in each field element (see EncodeBlobs), which could be unexpected for certain blobs
	j := 0
	for i := 0; i < params.BlobTxFieldElementsPerBlob; i++ {
		data = append(data, blob[j:j+31]...)
		j += 32
	}

	i := len(data) - 1
	for ; i >= 0; i-- {
		if data[i] != 0x00 {
			break
		}
	}
	data = data[:i+1]
	return data
}

func DecodeUint256String(hexOrDecimal string) (*uint256.Int, error) {
	var base = 10
	if strings.HasPrefix(hexOrDecimal, "0x") {
		base = 16
	}
	b, ok := new(big.Int).SetString(hexOrDecimal, base)
	if !ok {
		return nil, fmt.Errorf("invalid value")
	}
	val256, nok := uint256.FromBig(b)
	if nok {
		return nil, fmt.Errorf("value is too big")
	}
	return val256, nil
}
