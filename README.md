# Blobtoss

Made during Devconnect '23 @ Istanbul

> [Watch our presentation](http://34.134.77.77:3333/stream/video?slot=135474) stored directly in the beaconchain 🙂. The video size is 31MB and was divided into 251 blobs of 128KB each. Uploading at a rate of 6 blobs per transaction took 16m33s and costed us 32,899,072 Blob Gas on devnet-11.

CLI for messing around with multi-part blobs. Based on blob-utils by Inphi.

To get started run:

```
git clone https://github.com/Blob-Guardians/blobtoss-cli && cd blobtoss-cli
go build
./blobtoss-cli -help
```

## Features

- Upload multi-part blobs to beacon chain (`blob-utils tx`)
- Retrieve multi-part blobs from beacon chain (`blob-utils download`)
- Upload and serve multi-part blobs from beacon chain using the HTTP gateway (`blob-utils serve`)

```
blob-utils tx --private-key PRIV_KEY --chain-id 7011893061 --priority-gas-price 6000000000 --gas-price 800000000000 --max-fee-per-blob-gas 70000000000 --rpc-url http://10.128.0.8:8545 --to 0x0000000000000000000000000000000000000000 --blob-file DoD.jpg
./blob-utils download --slot 129252
```

Upload file using the `/upload` HTTP endpoint:

```
curl -X POST -H "Content-Type: multipart/form-data" -F "file=@test_files/whitepaper.html" http://localhost:3333/upload
```

## Known issues

* Video stream does not work on safari / ios
* A file can be split maximum into 256 blob pieces

## Credits

* https://github.com/Inphi/blob-utils
