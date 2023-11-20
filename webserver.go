package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli"
)

/*
Some examples:

Empty: http://localhost:3333/stream/html?slot=125751
Hello world: http://localhost:3333/stream/html?slot=125753
Html: http://localhost:3333/stream/html?slot=125754
Video: // http://localhost:3333/stream/html?slot=125754

Image: http://localhost:3333/stream/html?slot=129252
	   https://blobscan.com/tx/0xd148b4fad7e559687855b87c78d13a81c9777dcb06313b641f8ffc72177c7c7b
*/

var globalUploadParams BlobUploadParams

func streamHtmlHandler(w http.ResponseWriter, r *http.Request) {
	serveBlob(w, r, "text/html")
}

func streamVideoHandler(w http.ResponseWriter, r *http.Request) {
	serveBlob(w, r, "video/mp4")
}

func streamSvgHandler(w http.ResponseWriter, r *http.Request) {
	serveBlob(w, r, "image/svg+xml")
}

func streamImageHandler(w http.ResponseWriter, r *http.Request) {
	serveBlob(w, r, "image/jpeg")
}

func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// TODO: empty slot param
func serveBlob(w http.ResponseWriter, r *http.Request, contentType string) {
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.(http.Flusher).Flush()

	params, _ := url.ParseQuery(r.URL.RawQuery)
	fmt.Println("params", params)

	addr := "http://10.128.0.8:5052"
	slot := params.Get("slot")

	slotNumber, err := strconv.Atoi(slot)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	blobChannel := make(chan []byte)

	go GetMultiPartBlob(blobChannel, addr, slotNumber, false)

	fmt.Println("Waiting for blobChannel...")
	for {
		select {
		case result, ok := <-blobChannel:
			if !ok {
				fmt.Println("All blobs received.")
				return
			}
			fmt.Println("Blob received through channel")

			w.Write(result)
			w.(http.Flusher).Flush()
		}
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("/upload request")
	enableCORS(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	err := r.ParseMultipartForm(10 << 20) // 10 MB limit
	if err != nil {
		fmt.Println("Error parsing form")
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		fmt.Println("Error retrieving file")
		http.Error(w, "Error retrieving file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := "uploads/" + handler.Filename
	uploadedFile, err := os.Create(filename)
	if err != nil {
		fmt.Println("Error creating file on server")
		http.Error(w, "Error creating file on server", http.StatusInternalServerError)
		return
	}
	defer uploadedFile.Close()

	_, err = io.Copy(uploadedFile, file)
	if err != nil {
		http.Error(w, "Error copying file content", http.StatusInternalServerError)
		return
	}

	globalUploadParams.File = filename

	initialSlot, err := MultipartUpload(globalUploadParams)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Prepare JSON response
	response := map[string]string{
		"status":   "success",
		"message":  "File uploaded successfully",
		"filename": uploadedFile.Name(),
		"slot":     strconv.Itoa(int(initialSlot)),
	}

	// Convert the response to JSON
	jsonResponse, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "Error converting response to JSON", http.StatusInternalServerError)
		return
	}

	// Set the content type and write the JSON response
	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonResponse)
}

func WebserverApp(cliCtx *cli.Context) error {
	addr := cliCtx.String(TxRPCURLFlag.Name)
	prv := cliCtx.String(TxPrivateKeyFlag.Name)

	globalUploadParams = BlobUploadParams{
		Host:             addr,
		To:               common.HexToAddress("0x0000000000000000000000000000000000000000"),
		PrivateKey:       prv,
		Value:            "0x0",
		GasLimit:         21000,
		GasPrice:         "800000000000",
		PriorityGasPrice: "6000000000",
		MaxFeePerBlobGas: "70000000000",
		ChainID:          "7011893061",
		Calldata:         "0x",
		BlobsPerTx:       6,
	}

	http.HandleFunc("/stream/video", streamVideoHandler)
	http.HandleFunc("/stream/image", streamImageHandler)
	http.HandleFunc("/stream/svg", streamSvgHandler)
	http.HandleFunc("/stream/html", streamHtmlHandler)
	http.HandleFunc("/upload", uploadHandler)

	// addr := cliCtx.String(WebserverRPCURLFlag.Name)

	port := 3333
	fmt.Printf("Server listening on :%d\n", port)
	err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
	if err != nil {
		fmt.Println("Error starting server:", err)
	}
	return nil
}
