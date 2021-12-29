package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/civilware/Gnomon/graviton"
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
	txid       string
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
  --start-topoheight=<31170>	define a start topoheight other than 1 if required to index at a higher block (pruned db etc.)`

var rpc_client = &Client{}

var daemon_endpoint string
var blid string
var Connected bool = false
var Closing bool = false
var chain_topoheight int64
var last_indexedheight int64

var validated_scs []string

func main() {
	var err error

	SetupCloseHandler()

	// Initial set to 1 as topoheight 0 doesn't exist
	last_indexedheight = 1

	// Inspect argument(s)
	var arguments map[string]interface{}
	arguments, err = docopt.Parse(command_line, nil, true, "DERO Message Client : work in progress", false)

	if err != nil {
		log.Fatalf("[Main] Error while parsing arguments err: %s\n", err)
	}

	// Set variables from arguments
	daemon_endpoint = "127.0.0.1:40402"
	if arguments["--daemon-rpc-address"] != nil {
		daemon_endpoint = arguments["--daemon-rpc-address"].(string)
	}

	log.Printf("[Main] Using daemon RPC endpoint %s\n", daemon_endpoint)

	if arguments["--start-topoheight"] != nil {
		last_indexedheight, err = strconv.ParseInt(arguments["--start-topoheight"].(string), 10, 64)
		if err != nil {
			log.Fatalf("[Main] ERROR while converting --start-topoheight to int64")
		}
	}

	// Database - TODO: Not used or handled yet.. more for testing for the time being
	shasum := fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
	db_folder := fmt.Sprintf("%s_%s", "Gnomon", shasum)
	dbtrees := []string{"test1", "test2", "test3"}
	graviton.NewGravDB(dbtrees, db_folder, "25ms", 5000)

	// Simple connect loop .. if connection fails initially then keep trying, else break out and continue on. Connect() is handled in getInfo() for retries later on if connection ceases again
	for {
		err = Connect()
		if err != nil {
			continue
		}
		break
	}

	// Continuously getInfo from daemon to update topoheight globally
	go rpc_client.getInfo()
	time.Sleep(1 * time.Second)

	for {
		if Closing {
			// Holds in place until SetupCloseHandler() syncs and exits out
			select {}
		}

		if last_indexedheight > chain_topoheight {
			time.Sleep(1 * time.Second)
			continue
		}

		log.Printf("Checking topoheight %v / %v", last_indexedheight, chain_topoheight)

		blid, err = rpc_client.getBlockHash(uint64(last_indexedheight))
		if err != nil {
			log.Printf("[mainFOR] ERROR - %v", err)
			time.Sleep(1 * time.Second)
			continue
		}
		//log.Printf("BLID from getBlockHash(): %v", blid)

		err = rpc_client.indexBlock(blid)
		if err != nil {
			log.Printf("[mainFOR] ERROR - %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		last_indexedheight++
	}
}

func Connect() (err error) {
	rpc_client.WS, _, err = websocket.DefaultDialer.Dial("ws://"+daemon_endpoint+"/ws", nil)

	// notify user of any state change
	// if daemon connection breaks or comes live again
	if err == nil {
		if !Connected {
			log.Printf("[Connect] Connection to RPC server successful - ws://%s/ws", daemon_endpoint)
			Connected = true
		}
	} else {
		log.Printf("[Connect] ERROR connecting to daemon %v", err)

		if Connected {
			log.Printf("[Connect] ERROR - Connection to RPC server Failed - ws://%s/ws", daemon_endpoint)
		}
		Connected = false
		return err
	}

	input_output := rwc.New(rpc_client.WS)
	rpc_client.RPC = jrpc2.NewClient(channel.RawJSON(input_output, input_output), nil)

	return err
}

func (client *Client) indexBlock(blid string) (err error) {
	var io rpc.GetBlock_Result
	var ip = rpc.GetBlock_Params{Hash: blid}

	if err = rpc_client.RPC.CallResult(context.Background(), "DERO.GetBlock", ip, &io); err != nil {
		log.Printf("[indexBlock] ERROR - GetBlock failed: %v", err)
		return err
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
			log.Printf("[indexBlock] ERROR - GetTransaction failed: %v", err)
			return err
		} else {
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//log.Printf("%v", output)
		}

		tx_bin, _ := hex.DecodeString(output.Txs_as_hex[0])
		tx.Deserialize(tx_bin)

		if tx.TransactionType == transaction.SC_TX {
			sc_args = tx.SCDATA
			var method string
			var scid string

			entrypoint := fmt.Sprintf("%v", sc_args.Value("entrypoint", "S"))

			sc_action := fmt.Sprintf("%v", sc_args.Value("SC_ACTION", "U"))

			// Other ways to parse this, but will do for now --> see github.com/deroproject/derohe/blockchain/blockchain.go l.688
			if sc_action == "1" {
				method = "installsc"
				scid = string(bl.Tx_hashes[i].String())
			} else {
				method = "scinvoke"
				// Get "SC_ID" which is of type H to byte.. then to string
				scid_hex := []byte(fmt.Sprintf("%v", sc_args.Value("SC_ID", "H")))
				scid = string(scid_hex)
			}

			log.Printf("TX %v is a SC transaction!", bl.Tx_hashes[i])
			bl_sctxs = append(bl_sctxs, Parse{txid: bl.Tx_hashes[i].String(), scid: scid, entrypoint: entrypoint, method: method, sc_args: sc_args})
		} else {
			log.Printf("TX %v is NOT a SC transaction.", bl.Tx_hashes[i])
		}
	}

	if len(bl_sctxs) > 0 {
		log.Printf("Block %v has %v SC txs:", bl.GetHash(), len(bl_sctxs))

		for i := 0; i < len(bl_sctxs); i++ {
			if bl_sctxs[i].method == "installsc" {
				//log.Printf("%v", bl_sctxs[i].txid)

				code := fmt.Sprintf("%v", bl_sctxs[i].sc_args.Value("SC_CODE", "S"))

				// Temporary check - will need something more robust to code compare potentially all except InitializePrivate() with the template file.
				contains := strings.Contains(code, "200 STORE(\"artificerfee\", 1)")
				if !contains {
					// Then reject the validation that this is an artificer installsc action and move on
					log.Printf("SCID %v does not contain the match string for artificer, moving on.", bl_sctxs[i].scid)
				} else {
					// Append into db for artificer validated SC
					log.Printf("SCID %v matches artificer. This should be added to DB.", bl_sctxs[i].scid)
					validated_scs = append(validated_scs, bl_sctxs[i].scid)
				}
			} else {
				if scidExist(validated_scs, bl_sctxs[i].scid) {
					log.Printf("SCID %v is validated, checking the SC TX entrypoints to see if they should be logged.", bl_sctxs[i].scid)
					if bl_sctxs[i].entrypoint == "Start" {
						log.Printf("Tx %v matches scinvoke call of Start. This should be added to DB.", bl_sctxs[i].txid)
					} else {
						log.Printf("Tx %v does not match scinvoke call of Start, but %v instead. This should not (currently) be added to DB.", bl_sctxs[i].txid, bl_sctxs[i].entrypoint)
					}
				} else {
					log.Printf("SCID %v is not validated and thus we do not log SC interactions for this. Moving on.", bl_sctxs[i].scid)
				}
				//log.Printf("%v", bl_sctxs[i].scid)
				//log.Printf("%v", bl_sctxs[i].entrypoint)
			}
		}
	} else {
		log.Printf("Block %v does not have any SC txs", bl.GetHash())
	}

	return err
}

func scidExist(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

func (client *Client) getBlockHash(height uint64) (hash string, err error) {
	//log.Printf("[getBlockHash] Attempting to get block details at topoheight %v", height)

	var io rpc.GetBlockHeaderByHeight_Result
	var ip = rpc.GetBlockHeaderByTopoHeight_Params{TopoHeight: height}

	if err = client.RPC.CallResult(context.Background(), "DERO.GetBlockHeaderByTopoHeight", ip, &io); err != nil {
		log.Printf("[getBlockHash] GetBlockHeaderByTopoHeight failed: %v", err)
		return hash, err
	} else {
		//log.Printf("[getBlockHash] Retrieved block header from topoheight %v", height)
		//mainnet = !info.Testnet // inverse of testnet is mainnet
		//log.Printf("%v", io)
	}

	hash = io.Block_Header.Hash

	return hash, err
}

func (client *Client) getInfo() {
	for {
		var err error

		var info rpc.GetInfo_Result

		// collect all the data afresh,  execute rpc to service
		if err = rpc_client.RPC.CallResult(context.Background(), "DERO.GetInfo", nil, &info); err != nil {
			log.Printf("[getInfo] ERROR - GetInfo failed: %v", err)
			time.Sleep(1 * time.Second)
			Connect() // Attempt to re-connect now
			continue
		} else {
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//log.Printf("%v", info)
		}

		chain_topoheight = info.TopoHeight

		time.Sleep(5 * time.Second)
	}
}

// SetupCloseHandler creates a 'listener' on a new goroutine which will notify the
// program if it receives an interrupt from the OS. We then handle this by calling
// our clean up procedure and exiting the program.
// Reference: https://golangcode.com/handle-ctrl-c-exit-in-terminal/
func SetupCloseHandler() {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Printf("\r- Ctrl+C pressed in Terminal")
		log.Printf("Closing - syncing stats...")
		Closing = true

		// TODO: Log the last_indexedheight

		// Add 1 second sleep prior to closing to prevent db writing issues
		time.Sleep(time.Second)
		//Graviton_backend.DB.Close()
		os.Exit(0)
	}()
}
