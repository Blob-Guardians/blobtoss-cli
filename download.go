package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/urfave/cli"
)

type BlobResponse struct {
	Data []struct {
		Blob string `json:"blob"`
	} `json:"data"`
}

func appendToFile(filename string, data []byte) error {
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(data)
	if err != nil {
		return err
	}

	return nil
}

func GetMultiPartBlob(blobChannel chan<- []byte, addr string, initialSlot int, saveFiles bool) error {

	var blobIndex, totalBlobs int
	magicHeaderCustom := make([]byte, 8)

	slot := initialSlot
	filename := fmt.Sprintf("%d.blob", initialSlot)

	for {
		//fmt.Printf("Retrieving multi-part blob from slot %d\n", slot)
		apiURL := fmt.Sprintf("%s/eth/v1/beacon/blob_sidecars/%d", addr, slot)

		resp, err := http.Get(apiURL)
		if err != nil {
			fmt.Println("Error making HTTP request:", err)
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Println("Received non-OK status code:", resp.StatusCode)
			slot++
			continue
		}

		var responseObject BlobResponse
		err = json.NewDecoder(resp.Body).Decode(&responseObject)
		if err != nil {
			fmt.Println("Error decoding JSON:", err)
			return err
		}

		for _, item := range responseObject.Data {
			// fmt.Println("Retrieving blob index", idx)
			blobValue := item.Blob

			if blobValue[0:30] != "0x426c6f6273417265436f6d696e67" {
				//fmt.Println("Blob number", idx, "does not contain magic header:", blobValue[0:30])
				continue
			}

			hexBytes, err := hex.DecodeString(blobValue[2:])
			if err != nil {
				fmt.Println("Error decoding hex string:", err)
				return err
			}

			blobIndex = int(hexBytes[17])

			//fmt.Printf("Magic header: %v\n", hexBytes[0:32])
			//fmt.Printf("FULL BLOB:\n%v\n", hexBytes)

			if blobIndex == 0 {
				totalBlobs = int(hexBytes[19])
				copy(magicHeaderCustom, hexBytes[24:32])
			} else {
				if !bytes.Equal(magicHeaderCustom, hexBytes[24:32]) {
					// SKIP
					fmt.Println("Found blob with magic header but skipping because seed does not match.")
					continue
				}
			}

			cleanHexBytes := DecodeMagicBlob(hexBytes)

			fmt.Printf("[SLOT %d] Received blob %d of %d with size=%d\n", slot, blobIndex+1, totalBlobs, len(cleanHexBytes))

			blobChannel <- cleanHexBytes

			if saveFiles {
				err := appendToFile(filename, cleanHexBytes)
				if err != nil {
					fmt.Println("Error appending to file:", err)
					return err
				}
				fmt.Printf("Blob content written to '%s' successfully.\n", filename)
			}

			if blobIndex+1 == totalBlobs {
				fmt.Printf("%d blobs were retrieved in total\n", totalBlobs+1)
				if saveFiles {
					fmt.Printf("Hex bytes written to '%s' file successfully.\n", filename)
				}
				close(blobChannel)
				return nil
			}
		}

		slot++
	}

	//fmt.Println("Total blobs in this slot:", len(responseObject.Data))
}

func DownloadApp(cliCtx *cli.Context) error {
	startTime := time.Now()

	addr := cliCtx.String(DownloadBeaconRPCURLFlag.Name)
	slot := cliCtx.Int(DownloadSlotFlag.Name)

	blobChannel := make(chan []byte)

	go GetMultiPartBlob(blobChannel, addr, slot, true)

	for {
		select {
		case _, ok := <-blobChannel:
			if !ok {
				elapsedTime := time.Since(startTime)
				fmt.Println("Operation took", elapsedTime)
				return nil
			}
		}
	}

}
