package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/deroproject/derohe/rpc"
	"github.com/docopt/docopt-go"
	"github.com/ybbus/jsonrpc"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"
)

var walletRPCClient jsonrpc.RPCClient
var derodRPCClient jsonrpc.RPCClient
var scid string
var ringsize uint64
var prevTH int64
var pollTime time.Duration
var thAddition int64
var gnomonIndexes []*structures.GnomonSCIDQuery
var mux sync.Mutex
var version = "0.1a"

var command_line string = `Gnomon
Gnomon SC Index Registration Service: As the Gnomon SCID owner, you can automatically poll your local gnomon instance for new SCIDs to append to the index SC

Usage:
  gnomonsc [options]
  gnomonsc -h | --help

Options:
  -h --help     Show this screen.
  --daemon-rpc-address=<127.0.0.1:40402>	Connect to daemon rpc.
  --wallet-rpc-address=<127.0.0.1:40403>	Connect to wallet rpc.
  --gnomon-api-address=<127.0.0.1:8082>	Gnomon api to connect to.
  --block-deploy-buffer=<10>	Block buffer inbetween SC calls. This is for safety, will be hardcoded to minimum of 2 but can define here any amount (10 default).`

func main() {
	var err error

	//n := runtime.NumCPU()
	//runtime.GOMAXPROCS(n)

	pollTime, _ = time.ParseDuration("5s")
	ringsize = uint64(2)

	// Inspect argument(s)
	arguments, err := docopt.ParseArgs(command_line, nil, version)

	if err != nil {
		log.Fatalf("[Main] Error while parsing arguments err: %s\n", err)
	}

	// Set variables from arguments
	daemon_rpc_endpoint := "127.0.0.1:40402"
	if arguments["--daemon-rpc-address"] != nil {
		daemon_rpc_endpoint = arguments["--daemon-rpc-address"].(string)
	}

	log.Printf("[Main] Using daemon RPC endpoint %s\n", daemon_rpc_endpoint)

	gnomon_api_endpoint := "127.0.0.1:8082"
	if arguments["--gnomon-api-address"] != nil {
		gnomon_api_endpoint = arguments["--gnomon-api-address"].(string)
	}

	log.Printf("[Main] Using gnomon API endpoint %s\n", gnomon_api_endpoint)

	wallet_rpc_endpoint := "127.0.0.1:40403"
	if arguments["--wallet-rpc-address"] != nil {
		wallet_rpc_endpoint = arguments["--wallet-rpc-address"].(string)
	}

	log.Printf("[Main] Using wallet RPC endpoint %s\n", wallet_rpc_endpoint)

	thAddition = int64(10)
	if arguments["--block-deploy-buffer"] != nil {
		thAddition, err = strconv.ParseInt(arguments["--block-deploy-buffer"].(string), 10, 64)
		if err != nil {
			log.Fatalf("[Main] ERROR while converting --block-deploy-buffer to int64\n")
			return
		}
		if thAddition < 2 {
			thAddition = int64(2)
		}
	}

	log.Printf("[Main] Using block deploy buffer of '%v' blocks.\n", thAddition)

	// wallet/derod rpc clients
	walletRPCClient = jsonrpc.NewClient("http://" + wallet_rpc_endpoint + "/json_rpc")
	derodRPCClient = jsonrpc.NewClient("http://" + daemon_rpc_endpoint + "/json_rpc")

	// Get testnet/mainnet
	var info rpc.GetInfo_Result
	err = derodRPCClient.CallFor(&info, "get_info")
	if err != nil {
		log.Printf("ERR: %v", err)
		return
	}

	// SCID
	switch info.Testnet {
	case false:
		scid = "a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4"
	case true:
		scid = "c9d23d2fc3aaa8e54e238a2218c0e5176a6e48780920fd8474fac5b0576110a2"
	}

	for {
		fetchGnomonIndexes(gnomon_api_endpoint)
		runGnomonIndexer(daemon_rpc_endpoint)
		log.Printf("[Main] Round completed. Sleeping 1 minute for next round.")
		time.Sleep(60 * time.Second)
	}
}

func fetchGnomonIndexes(gnomonendpoint string) {
	mux.Lock()
	defer mux.Unlock()
	var lastQuery map[string]interface{}
	var err error
	log.Printf("[fetchGnomonIndexes] Getting sc data")
	rs, err := http.Get("http://" + gnomonendpoint + "/api/indexedscs")
	if err != nil {
		log.Printf("[fetchGnomonIndexes] gnomon query err %s\n", err)
	} else {
		log.Printf("[fetchGnomonIndexes] Retrieved sc data... reading in and building structures.")
		b, err := io.ReadAll(rs.Body)
		if err != nil {
			log.Printf("[fetchGnomonIndexes] error reading body %s\n", err)
		} else {
			err = json.Unmarshal(b, &lastQuery)
			if err != nil {
				log.Printf("[fetchGnomonIndexes] error unmarshalling b %s\n", err)
			}

			if lastQuery["indexdetails"] != nil {
				var changes []*structures.GnomonSCIDQuery
				for _, v := range lastQuery["indexdetails"].([]interface{}) {
					x := v.(map[string]interface{})
					//log.Printf("inputscid(\"%v\", \"%v\", %v)", x["SCID"], x["Owner"], x["Height"])
					height := x["Height"].(float64)
					changes = append(changes, &structures.GnomonSCIDQuery{Owner: x["Owner"].(string), Height: uint64(height), SCID: x["SCID"].(string)})
				}
				gnomonIndexes = changes
			}
		}
	}
}

func runGnomonIndexer(derodendpoint string) {
	mux.Lock()
	defer mux.Unlock()
	log.Printf("[runGnomonIndexer] Provisioning new RAM indexer...")
	var Graviton_backend *storage.GravitonStore
	Graviton_backend = storage.NewGravDBRAM("25ms")
	defaultIndexer := indexer.NewIndexer(Graviton_backend, "", int64(1), derodendpoint, "daemon", false, false, true)
	defaultIndexer.StartDaemonMode()

	for {
		if len(gnomonIndexes) == 0 || defaultIndexer.ChainHeight <= 1 || defaultIndexer.LastIndexedHeight < defaultIndexer.ChainHeight {
			log.Printf("[runGnomonIndexer] Waiting on gnomonIndexes or defaultIndexer... (%v / %v) - len(%v)", defaultIndexer.LastIndexedHeight, defaultIndexer.ChainHeight, len(gnomonIndexes))
			time.Sleep(5 * time.Second)
		} else {
			break
		}
	}

	var changes bool
	var variables []*structures.SCIDVariable
	variables, _, _ = defaultIndexer.RPC.GetSCVariables(scid, defaultIndexer.ChainHeight)
	log.Printf("[runGnomonIndexer] Looping through discovered SCs and checking to see if any are not indexed.")
	for _, v := range gnomonIndexes {
		i := 0
		valuesstringbykey, valuesuint64bykey := defaultIndexer.GetSCIDValuesByKey(variables, scid, v.SCID+"height", defaultIndexer.ChainHeight)
		if len(valuesstringbykey) > 0 {
			i++
		}
		if len(valuesuint64bykey) > 0 {
			i++
		}

		if i == 0 {
			changes = true
			log.Printf("[runGnomonIndexer] SCID has not been indexed - %v ... Indexing now", v.SCID)
			// Do indexing job here.
			// TODO: Support for authenticator/user:password rpc login for wallet interactions
			inputscid(v.SCID, v.Owner, v.Height)
		}
	}
	if !changes {
		log.Printf("[runGnomonIndexer] No changes made.")
	}

	log.Printf("[runGnomonIndexer] Closing temporary indexer...")
	defaultIndexer.Close()
	time.Sleep(5 * time.Second)
	log.Printf("[runGnomonIndexer] Indexer closed.")
}

func inputscid(inpscid string, scowner string, deployheight uint64) {
	// Get gas estimate based on updatecode function to calculate appropriate storage fees to append
	var rpcArgs = rpc.Arguments{}
	rpcArgs = append(rpcArgs, rpc.Argument{Name: "entrypoint", DataType: "S", Value: "InputSCID"})
	rpcArgs = append(rpcArgs, rpc.Argument{Name: "scid", DataType: "S", Value: inpscid})
	rpcArgs = append(rpcArgs, rpc.Argument{Name: "scowner", DataType: "S", Value: scowner})
	rpcArgs = append(rpcArgs, rpc.Argument{Name: "deployheight", DataType: "U", Value: deployheight})
	var transfers []rpc.Transfer

	sendtx(rpcArgs, transfers)
}

func sendtx(rpcArgs rpc.Arguments, transfers []rpc.Transfer) {
	var err error
	var gasstr rpc.GasEstimate_Result
	var addr rpc.GetAddress_Result
	err = walletRPCClient.CallFor(&addr, "GetAddress")
	if addr.Address == "" {
		log.Printf("[GetAddress] Failed - %v", err)
	}
	gasRpc := rpcArgs
	gasRpc = append(gasRpc, rpc.Argument{Name: "SC_ACTION", DataType: "U", Value: rpc.SC_CALL})
	gasRpc = append(gasRpc, rpc.Argument{Name: "SC_ID", DataType: "H", Value: string([]byte(scid))})

	var gasestimateparams rpc.GasEstimate_Params
	if len(transfers) > 0 {
		if ringsize > 2 {
			gasestimateparams = rpc.GasEstimate_Params{SC_RPC: gasRpc, Ringsize: ringsize, Signer: "", Transfers: transfers}
		} else {
			gasestimateparams = rpc.GasEstimate_Params{SC_RPC: gasRpc, Ringsize: ringsize, Signer: addr.Address, Transfers: transfers}
		}
	} else {
		if ringsize > 2 {
			gasestimateparams = rpc.GasEstimate_Params{SC_RPC: gasRpc, Ringsize: ringsize, Signer: ""}
		} else {
			gasestimateparams = rpc.GasEstimate_Params{SC_RPC: gasRpc, Ringsize: ringsize, Signer: addr.Address}
		}
	}
	err = derodRPCClient.CallFor(&gasstr, "DERO.GetGasEstimate", gasestimateparams)
	if err != nil {
		log.Printf("[getGasEstimate] gas estimate err %s\n", err)
		return
	} else {
		log.Printf("[getGasEstimate] gas estimate results: %v", gasstr)
	}
	var txnp rpc.Transfer_Params
	var str rpc.Transfer_Result

	txnp.SC_RPC = gasRpc
	if len(transfers) > 0 {
		txnp.Transfers = transfers
	}
	txnp.Ringsize = ringsize
	txnp.Fees = gasstr.GasStorage

	// Loop through to ensure we haven't recently sent in this session too quickly
	if prevTH != 0 {
		for {
			var info rpc.GetInfo_Result
			err := derodRPCClient.CallFor(&info, "get_info")
			if err != nil {
				log.Printf("ERR: %v", err)
				return
			}

			targetTH := prevTH + thAddition

			if targetTH <= info.TopoHeight {
				prevTH = info.TopoHeight
				break
			} else {
				log.Printf("[sendTX] Waiting until topoheights line up to send next TX [last: %v / curr: %v]", info.TopoHeight, targetTH)
				time.Sleep(pollTime)
			}
		}
	} else {
		var info rpc.GetInfo_Result
		err := derodRPCClient.CallFor(&info, "get_info")
		if err != nil {
			log.Printf("ERR: %v", err)
			return
		}

		prevTH = info.TopoHeight
	}

	// Call Transfer (not scinvoke) since we append fees above like a normal txn.

	err = walletRPCClient.CallFor(&str, "Transfer", txnp)
	if err != nil {
		log.Printf("[sendTx] err: %v", err)
		return
	} else {
		log.Printf("[sendTx] Tx sent successfully - txid: %v", str.TXID)
	}
}
