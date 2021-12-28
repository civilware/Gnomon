package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"strings"

	"github.com/civilware/Gnomon/rwc"
	"github.com/docopt/docopt-go"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/gorilla/websocket"
)

type Client struct {
	WS  *websocket.Conn
	RPC *jrpc2.Client
}

type Parse struct {
	scid       string
	entrypoint string
	method     string
	sc_args    rpc.Arguments
}

var command_line string = `Gnomon
Gnomon Indexing Service: Index DERO's blockchain for Artificer NFT deployments/listings/etc.

Usage:
  gnomon [options]
  gnomon -h | --help

Options:
  -h --help     Show this screen.
  --daemon-rpc-address=<127.0.0.1:40402>	connect to daemon
  --blid=<1c1ce37ed1726f8626566f7e1dbb6e8855bd620a2aea4683f001708126792dce>		block id to query (example blid may not be present in the public chain)`

var rpc_client = &Client{}

var daemon_endpoint string
var blid string
var Connected bool = false

func main() {
	var err error

	var arguments map[string]interface{}
	arguments, err = docopt.Parse(command_line, nil, true, "DERO Message Client : work in progress", false)
	//_ = arguments
	if err != nil {
		log.Fatalf("[Main] Error while parsing arguments err: %s\n", err)
	}

	// Set variables from arguments
	daemon_endpoint = "127.0.0.1:40402"
	if arguments["--daemon-rpc-address"] != nil {
		daemon_endpoint = arguments["--daemon-rpc-address"].(string)
	}

	log.Printf("[Main] Using daemon RPC endpoint %s\n", daemon_endpoint)

	if arguments["--blid"] != nil {
		blid = arguments["--blid"].(string)
	} else {
		log.Fatalf("[Main] No blid defined.")
		return
	}

	rpc_client.WS, _, err = websocket.DefaultDialer.Dial("ws://"+daemon_endpoint+"/ws", nil)

	// notify user of any state change
	// if daemon connection breaks or comes live again
	if err == nil {
		if !Connected {
			log.Printf("Connection to RPC server successful - ws://%s/ws", daemon_endpoint)
			Connected = true
		}
	} else {
		log.Fatalf("Error connecting to daemon %v", err)

		if Connected {
			log.Fatalf("Connection to RPC server Failed - ws://%s/ws", daemon_endpoint)
		}
		Connected = false
		return
	}

	input_output := rwc.New(rpc_client.WS)
	rpc_client.RPC = jrpc2.NewClient(channel.RawJSON(input_output, input_output), nil)

	var result string
	if err := rpc_client.RPC.CallResult(context.Background(), "DERO.Ping", nil, &result); err != nil {
		log.Fatalf("Ping failed: %v", err)
	} else {
		//		fmt.Printf("Ping Received %s\n", result)
	}

	var info rpc.GetInfo_Result

	// collect all the data afresh,  execute rpc to service
	if err = rpc_client.RPC.CallResult(context.Background(), "DERO.GetInfo", nil, &info); err != nil {
		log.Fatalf("GetInfo failed: %v", err)
	} else {
		//mainnet = !info.Testnet // inverse of testnet is mainnet
		log.Printf("%v", info)
	}

	var io rpc.GetBlock_Result
	var ip = rpc.GetBlock_Params{Hash: blid}

	if err = rpc_client.RPC.CallResult(context.Background(), "DERO.GetBlock", ip, &io); err != nil {
		log.Fatalf("GetBlock failed: %v", err)
	} else {
		//mainnet = !info.Testnet // inverse of testnet is mainnet
		//log.Printf("%v", io)
	}

	var bl block.Block
	var block_bin []byte

	block_bin, _ = hex.DecodeString(io.Blob)
	bl.Deserialize(block_bin)

	var bl_sctxs []Parse //[]string

	for i := 0; i < len(bl.Tx_hashes); i++ {
		var tx transaction.Transaction
		var sc_args rpc.Arguments
		log.Printf("Checking tx - %v", bl.Tx_hashes[i])

		var inputparam rpc.GetTransaction_Params
		var output rpc.GetTransaction_Result

		inputparam.Tx_Hashes = append(inputparam.Tx_Hashes, bl.Tx_hashes[i].String())

		if err = rpc_client.RPC.CallResult(context.Background(), "DERO.GetTransaction", inputparam, &output); err != nil {
			log.Fatalf("GetTransaction failed: %v", err)
		} else {
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//log.Printf("%v", output)
		}

		tx_bin, _ := hex.DecodeString(output.Txs_as_hex[0])
		tx.Deserialize(tx_bin)

		if tx.TransactionType == transaction.SC_TX {
			sc_args = tx.SCDATA
			var method string

			entrypoint := fmt.Sprintf("%v", sc_args.Value("entrypoint", "S"))

			sc_action := fmt.Sprintf("%v", sc_args.Value("SC_ACTION", "U"))

			// Other ways to parse this, but will do for now --> see github.com/deroproject/derohe/blockchain/blockchain.go l.688
			if sc_action == "1" {
				method = "installsc"
			} else {
				method = "scinvoke"
			}

			log.Printf("TX %v is a SC transaction!", bl.Tx_hashes[i])
			bl_sctxs = append(bl_sctxs, Parse{scid: bl.Tx_hashes[i].String(), entrypoint: entrypoint, method: method, sc_args: sc_args})
		} else {
			log.Printf("TX %v is NOT a SC transaction.", bl.Tx_hashes[i])
		}
	}

	if len(bl_sctxs) > 0 {
		log.Printf("Block %v has %v SC txs:", bl.GetHash(), len(bl_sctxs))

		for i := 0; i < len(bl_sctxs); i++ {
			if bl_sctxs[i].method == "installsc" {
				//log.Printf("%v", bl_sctxs[i].scid)

				code := fmt.Sprintf("%v", bl_sctxs[i].sc_args.Value("SC_CODE", "S"))

				// Temporary check - will need something more robust to code compare potentially all except InitializePrivate() with the template file.
				contains := strings.Contains(code, "200 STORE(\"artificerfee\", 1)")
				if !contains {
					// Then reject the validation that this is an artificer installsc action and move on
					log.Printf("Tx %v does not contain the match string for artificer, moving on.", bl_sctxs[i].scid)
				} else {
					// Append into db for artificer validated SC
					log.Printf("Tx %v matches artificer. This should be added to DB.", bl_sctxs[i].scid)
				}
			} else {
				log.Printf("%v", bl_sctxs[i].scid)
			}
		}
	} else {
		log.Printf("Block %v does not have any SC txs", bl.GetHash())
	}
}
