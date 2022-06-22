package indexer

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/civilware/Gnomon/mbllookup"
	"github.com/civilware/Gnomon/rwc"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/cryptography/crypto"
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

type Indexer struct {
	LastIndexedHeight int64
	ChainHeight       int64
	SearchFilter      string
	Backend           *storage.GravitonStore
	Closing           bool
	RPC               *Client
	Endpoint          string
	RunMode           string
	MBLLookup         bool
	ValidatedSCs      []string
	CloseOnDisconnect bool
}

var daemon_endpoint string
var Connected bool = false

var chain_topoheight int64

func NewIndexer(Graviton_backend *storage.GravitonStore, search_filter string, last_indexedheight int64, endpoint string, runmode string, mbllookup bool, closeondisconnect bool) *Indexer {
	return &Indexer{
		LastIndexedHeight: last_indexedheight,
		SearchFilter:      search_filter,
		Backend:           Graviton_backend,
		RPC:               &Client{},
		Endpoint:          endpoint,
		RunMode:           runmode,
		MBLLookup:         mbllookup,
		CloseOnDisconnect: closeondisconnect,
	}
}

func (indexer *Indexer) StartDaemonMode() {
	var err error

	// Simple connect loop .. if connection fails initially then keep trying, else break out and continue on. Connect() is handled in getInfo() for retries later on if connection ceases again
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		log.Printf("Trying to connect...")
		err = indexer.RPC.Connect(indexer.Endpoint)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}
		break
	}
	time.Sleep(1 * time.Second)
	//log.Printf("%v", indexer.RPC.WS)

	// Continuously getInfo from daemon to update topoheight globally
	go indexer.getInfo()
	time.Sleep(1 * time.Second)

	storedindex := indexer.Backend.GetLastIndexHeight()
	if storedindex > indexer.LastIndexedHeight {
		log.Printf("[Main] Continuing from last indexed height %v\n", storedindex)
		indexer.LastIndexedHeight = storedindex

		// We can also assume this check to mean we have stored validated SCs potentially. TODO: Do we just get stored SCs regardless of sync cycle?
		//pre_validatedSCIDs := make(map[string]string)
		pre_validatedSCIDs := indexer.Backend.GetAllOwnersAndSCIDs()

		if len(pre_validatedSCIDs) > 0 {
			log.Printf("[Main] Appending pre-validated SCIDs from store to memory.\n")

			for k := range pre_validatedSCIDs {
				indexer.ValidatedSCs = append(indexer.ValidatedSCs, k)
			}
		}
	}

	// TODO: Dynamically get SCIDs of hardcoded SCs and append them if search filter is ""
	var hardcodedscids []string
	hardcodedscids = append(hardcodedscids, "0000000000000000000000000000000000000000000000000000000000000001")

	for _, vi := range hardcodedscids {
		if scidExist(indexer.ValidatedSCs, vi) {
			// Hardcoded SCID already exists, no need to re-add
			continue
		}
		var getSCResults rpc.GetSC_Result
		getSCParams := rpc.GetSC_Params{SCID: vi, Code: true, Variables: false, TopoHeight: indexer.LastIndexedHeight}
		if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetSC", getSCParams, &getSCResults); err != nil {
			//log.Printf("[getSCVariables] ERROR - getSCVariables failed: %v\n", err)
		}

		var contains bool

		// If we can get the SC and searchfilter is "" (get all), contains is true. Otherwise evaluate code against searchfilter
		if indexer.SearchFilter == "" && err != nil {
			contains = true
		} else {
			contains = strings.Contains(getSCResults.Code, indexer.SearchFilter)
		}

		if contains {
			indexer.ValidatedSCs = append(indexer.ValidatedSCs, vi)
			writeWait, _ := time.ParseDuration("50ms")
			for indexer.Backend.Writing == 1 {
				//log.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
				time.Sleep(writeWait)
			}
			indexer.Backend.Writing = 1
			err = indexer.Backend.StoreOwner(vi, "")
			if err != nil {
				log.Printf("Error storing owner: %v\n", err)
			}
			scVars := indexer.RPC.getSCVariables(vi, indexer.ChainHeight)
			err = indexer.Backend.StoreSCIDVariableDetails(vi, scVars, indexer.ChainHeight)
			if err != nil {
				log.Printf("[AddSCIDToIndex] ERR - storing scid variable details: %v\n", err)
			}
			err = indexer.Backend.StoreSCIDInteractionHeight(vi, indexer.ChainHeight)
			if err != nil {
				log.Printf("[AddSCIDToIndex] ERR - storing scid interaction height: %v\n", err)
			}
			indexer.Backend.Writing = 0
		}
	}

	go func() {
		for {
			if indexer.Closing {
				// Break out on closing call
				break
			}

			if indexer.LastIndexedHeight > indexer.ChainHeight {
				time.Sleep(1 * time.Second)
				continue
			}

			blid, err := indexer.RPC.getBlockHash(uint64(indexer.LastIndexedHeight))
			if err != nil {
				// Handle pruned nodes index errors... find height that they have blocks able to be indexed
				//log.Printf("Checking if strings contain: %v", err.Error())
				if strings.Contains(err.Error(), "err occured empty block") {
					currIndex := indexer.LastIndexedHeight
					rewindIndex := int64(0)
					blockJump := int64(10000)
					for {
						if indexer.Closing {
							// If we do concurrent blocks in the future, this will need to move/be modified to be *after* all concurrent blocks are done incase exit etc.
							writeWait, _ := time.ParseDuration("50ms")
							for indexer.Backend.Writing == 1 {
								//log.Printf("[StartDaemonMode-indexBlockgofunc] GravitonDB is writing... sleeping for %v...", writeWait)
								time.Sleep(writeWait)
							}
							indexer.Backend.Writing = 1
							indexer.Backend.StoreLastIndexHeight(currIndex)
							indexer.Backend.Writing = 0
							// Break out on closing call
							break
						}
						_, err = indexer.RPC.getBlockHash(uint64(currIndex))
						if err != nil {
							//if strings.Contains(err.Error(), "err occured empty block") {
							//time.Sleep(200 * time.Millisecond)	// sleep for node spam, not *required* but can be useful for lesser nodes in brief catchup time.
							// Increase block by 10 to not spam the daemon at every single block, but skip along a little bit to move faster/more less impact to node. This can be modified if required.
							if (currIndex + blockJump) > indexer.ChainHeight {
								currIndex = indexer.ChainHeight
							} else {
								currIndex += blockJump
							}
							log.Printf("GetBlock failed - checking %v", currIndex)
							//}
						} else {
							// Self-contain and loop through at most 10 or X blocks
							log.Printf("GetBlock worked at %v", currIndex)
							for {
								if indexer.Closing {
									// If we do concurrent blocks in the future, this will need to move/be modified to be *after* all concurrent blocks are done incase exit etc.
									writeWait, _ := time.ParseDuration("50ms")
									for indexer.Backend.Writing == 1 {
										//log.Printf("[StartDaemonMode-indexBlockgofunc] GravitonDB is writing... sleeping for %v...", writeWait)
										time.Sleep(writeWait)
									}
									indexer.Backend.Writing = 1
									indexer.Backend.StoreLastIndexHeight(rewindIndex)
									indexer.Backend.Writing = 0

									// Break out on closing call
									break
								}
								if rewindIndex == 0 {
									rewindIndex = currIndex - blockJump + 1
								} else {
									log.Printf("Checking GetBlock at %v", rewindIndex)
									_, err = indexer.RPC.getBlockHash(uint64(rewindIndex))
									if err != nil {
										rewindIndex++
										//time.Sleep(200 * time.Millisecond)	// sleep for node spam, not *required* but can be useful for lesser nodes in brief catchup time.
									} else {
										log.Printf("GetBlock worked at %v - continuing as normal", rewindIndex+1)
										// Break out, we found the earliest block detail
										indexer.LastIndexedHeight = rewindIndex + 1
										break
									}
								}
							}
							break
						}
					}
				}

				log.Printf("[mainFOR] ERROR - %v\n", err)
				time.Sleep(1 * time.Second)
				continue
			}

			err = indexer.indexBlock(blid, indexer.LastIndexedHeight, indexer.SearchFilter, indexer.MBLLookup, indexer.Backend)
			if err != nil {
				log.Printf("[mainFOR] ERROR - %v\n", err)
				time.Sleep(time.Second)
				continue
			}

			// If we do concurrent blocks in the future, this will need to move/be modified to be *after* all concurrent blocks are done incase exit etc.
			writeWait, _ := time.ParseDuration("50ms")
			for indexer.Backend.Writing == 1 {
				//log.Printf("[StartDaemonMode-indexBlockgofunc] GravitonDB is writing... sleeping for %v...", writeWait)
				time.Sleep(writeWait)
			}
			indexer.Backend.Writing = 1
			indexer.Backend.StoreLastIndexHeight(indexer.LastIndexedHeight)
			indexer.Backend.Writing = 0

			indexer.LastIndexedHeight++
		}
	}()
}

func (indexer *Indexer) StartWalletMode(runType string) {
	var err error

	// Simple connect loop .. if connection fails initially then keep trying, else break out and continue on. Connect() is handled in getInfo() for retries later on if connection ceases again
	go func() {
		for {
			err = indexer.RPC.Connect(indexer.Endpoint)
			if err != nil {
				continue
			}
			break
		}
	}()
	time.Sleep(1 * time.Second)

	// Continuously getInfo from daemon to update topoheight globally
	switch runType {
	case "receive":
		// do receive actions here (e.g. from data source via API/WS)
		// TODO: is there anything we need to do within indexer itself if just receiving?
	default:
		// 'retrieve'/etc.
		go indexer.getWalletHeight()
		time.Sleep(1 * time.Second)

		go func() {
			for {
				if indexer.Closing {
					// Break out on closing call
					break
				}

				if indexer.LastIndexedHeight > indexer.ChainHeight {
					time.Sleep(1 * time.Second)
					continue
				}

				// Do indexing calls here

				// TODO: Modify this to be the height of the *next* tx index etc.
				indexer.LastIndexedHeight++
			}
		}()
	}

	// Hold
	select {}
}

// Manually add/inject a SCID to be indexed. Checks validity and then stores within owner tree (no signer addr) and stores a set of current variables.
func (indexer *Indexer) AddSCIDToIndex(scid []string) (err error) {
	for _, v := range scid {
		// Check if already validated
		if scidExist(indexer.ValidatedSCs, v) {
			log.Printf("[AddSCIDToIndex] SCID '%v' already in validated list.", v)
			continue
		} else {
			// Validate SCID is *actually* a valid SCID
			scVars := indexer.RPC.getSCVariables(v, indexer.ChainHeight)

			// By returning valid variables of a given Scid (GetSC --> parse vars), we can conclude it is a valid SCID. Otherwise, skip adding to validated scids
			if len(scVars) > 0 {
				indexer.ValidatedSCs = append(indexer.ValidatedSCs, v)
				writeWait, _ := time.ParseDuration("50ms")
				for indexer.Backend.Writing == 1 {
					//log.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}
				indexer.Backend.Writing = 1
				err = indexer.Backend.StoreOwner(v, "")
				if err != nil {
					log.Printf("[AddSCIDToIndex] ERR - storing owner: %v\n", err)
				}
				err = indexer.Backend.StoreSCIDVariableDetails(v, scVars, indexer.ChainHeight)
				if err != nil {
					log.Printf("[AddSCIDToIndex] ERR - storing scid variable details: %v\n", err)
				}
				err = indexer.Backend.StoreSCIDInteractionHeight(v, indexer.ChainHeight)
				if err != nil {
					log.Printf("[AddSCIDToIndex] ERR - storing scid interaction height: %v\n", err)
				}
				indexer.Backend.Writing = 0
			} else {
				log.Printf("[AddSCIDToIndex] ERR - SCID '%v' doesn't exist at height %v", v, indexer.ChainHeight)
			}
		}
	}

	return err
}

func (client *Client) Connect(endpoint string) (err error) {
	client.WS, _, err = websocket.DefaultDialer.Dial("ws://"+endpoint+"/ws", nil)

	// notify user of any state change
	// if daemon connection breaks or comes live again
	if err == nil {
		if !Connected {
			log.Printf("[Connect] Connection to RPC server successful - ws://%s/ws\n", endpoint)
			Connected = true
		}
	} else {
		log.Printf("[Connect] ERROR connecting to daemon %v\n", err)

		if Connected {
			log.Printf("[Connect] ERROR - Connection to RPC server Failed - ws://%s/ws\n", endpoint)
		}
		Connected = false
		return err
	}

	input_output := rwc.New(client.WS)
	client.RPC = jrpc2.NewClient(channel.RawJSON(input_output, input_output), nil)

	return err
}

func (indexer *Indexer) indexBlock(blid string, topoheight int64, search_filter string, mbl bool, Graviton_backend *storage.GravitonStore) (err error) {
	var io rpc.GetBlock_Result
	var ip = rpc.GetBlock_Params{Hash: blid}

	if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetBlock", ip, &io); err != nil {
		return fmt.Errorf("[indexBlock] ERROR - GetBlock failed: %v\n", err)
	}

	var bl block.Block
	var block_bin []byte

	block_bin, _ = hex.DecodeString(io.Blob)
	bl.Deserialize(block_bin)

	if mbl {
		mbldetails, err2 := mbllookup.GetMBLByBLHash(bl)
		if err2 != nil {
			log.Printf("Error getting miniblock details for blid %v", bl.GetHash().String())
			return err2
		}

		writeWait, _ := time.ParseDuration("50ms")
		for Graviton_backend.Writing == 1 {
			//log.Printf("[Indexer-indexBlock-storeminiblockdetails] GravitonDB is writing... sleeping for %v...", writeWait)
			time.Sleep(writeWait)
		}
		Graviton_backend.Writing = 1
		err2 = Graviton_backend.StoreMiniblockDetailsByHash(blid, mbldetails)
		if err2 != nil {
			log.Printf("Error storing miniblock details for blid %v", err2)
			return err2
		}
		Graviton_backend.Writing = 0
	}

	var bl_sctxs []structures.SCTXParse
	var bl_normtxs []structures.NormalTXWithSCIDParse
	var regTxCount int64
	var normTxCount int64
	var burnTxCount int64

	var wg sync.WaitGroup
	wg.Add(len(bl.Tx_hashes))

	for i := 0; i < len(bl.Tx_hashes); i++ {
		go func(i int) {
			var tx transaction.Transaction
			var sc_args rpc.Arguments
			var sc_fees uint64
			var sender string

			var inputparam rpc.GetTransaction_Params
			var output rpc.GetTransaction_Result

			inputparam.Tx_Hashes = append(inputparam.Tx_Hashes, bl.Tx_hashes[i].String())

			if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetTransaction", inputparam, &output); err != nil {
				//log.Printf("[indexBlock] ERROR - GetTransaction for txid '%v' failed: %v\n", inputparam.Tx_Hashes, err)
				//return err
				//continue
				wg.Done()
				// If we error, this could be due to regtxn not valid on pruned node or other reasons. We will just nil the err and then return and move on.
				err = nil
				return
				// TODO - fix/handle/ensure
				//log.Printf("Node corruption at height '%v', continuing", topoheight)
				//return
			}

			tx_bin, _ := hex.DecodeString(output.Txs_as_hex[0])
			tx.Deserialize(tx_bin)

			// TODO: Add count for registration TXs and store the following on normal txs: IF SCID IS PRESENT, store tx details + ring members + fees + etc. Use later for scid balance queries
			if tx.TransactionType == transaction.SC_TX {
				sc_args = tx.SCDATA
				sc_fees = tx.Fees()
				var method string
				var scid string
				var scid_hex []byte

				entrypoint := fmt.Sprintf("%v", sc_args.Value("entrypoint", "S"))

				sc_action := fmt.Sprintf("%v", sc_args.Value("SC_ACTION", "U"))

				// Other ways to parse this, but will do for now --> see https://github.com/deroproject/derohe/blob/main/blockchain/blockchain.go#L688
				if sc_action == "1" {
					method = "installsc"
					scid = string(bl.Tx_hashes[i].String())
					scid_hex = []byte(scid)
				} else {
					method = "scinvoke"
					// Get "SC_ID" which is of type H to byte.. then to string
					scid_hex = []byte(fmt.Sprintf("%v", sc_args.Value("SC_ID", "H")))
					scid = string(scid_hex)
				}

				// TODO: What if there are multiple payloads with potentially different ringsizes, can that happen?
				if tx.Payloads[0].Statement.RingSize == 2 {
					sender = output.Txs[0].Signer
				} else {
					log.Printf("ERR - Ringsize for %v is != 2. Storing blank value for txid sender.\n", bl.Tx_hashes[i])
					//continue
					if method == "installsc" {
						// We do not store a ringsize > 2 of installsc calls. Only of SC interactions via sc_invoke for ringsize > 2 and just blank out the sender
						//continue
						wg.Done()
					}
				}
				//time.Sleep(2 * time.Second)
				bl_sctxs = append(bl_sctxs, structures.SCTXParse{Txid: bl.Tx_hashes[i].String(), Scid: scid, Scid_hex: scid_hex, Entrypoint: entrypoint, Method: method, Sc_args: sc_args, Sender: sender, Payloads: tx.Payloads, Fees: sc_fees, Height: topoheight})
			} else if tx.TransactionType == transaction.REGISTRATION {
				regTxCount++
			} else if tx.TransactionType == transaction.BURN_TX {
				// TODO: Handle burn_tx here
				burnTxCount++
			} else if tx.TransactionType == transaction.NORMAL {
				// TODO: Handle normal tx here
				normTxCount++

				for j := 0; j < len(tx.Payloads); j++ {
					var zhash crypto.Hash
					if tx.Payloads[j].SCID != zhash {
						log.Printf("TXID '%v' has SCID in payload of '%v' and ring members: %v.", bl.Tx_hashes[i], tx.Payloads[j].SCID, output.Txs[j].Ring[j])
						for _, v := range output.Txs[0].Ring[j] {
							//bl_normtxs = append(bl_normtxs, structures.NormalTXWithSCIDParse{Txid: bl.Tx_hashes[i].String(), Scid: tx.Payloads[j].SCID.String(), Fees: tx_fees, Height: int64(bl.Height)})
							writeWait, _ := time.ParseDuration("50ms")
							for Graviton_backend.Writing == 1 {
								//log.Printf("[Indexer-indexBlock-normTx-txLoop] GravitonDB is writing... sleeping for %v...", writeWait)
								time.Sleep(writeWait)
							}
							Graviton_backend.Writing = 1
							Graviton_backend.StoreNormalTxWithSCIDByAddr(v, &structures.NormalTXWithSCIDParse{Txid: bl.Tx_hashes[i].String(), Scid: tx.Payloads[j].SCID.String(), Fees: sc_fees, Height: int64(bl.Height)})
							Graviton_backend.Writing = 0
						}
					}
				}
			} else {
				//log.Printf("TX %v type is NOT handled.\n", bl.Tx_hashes[i])
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	//blheight := int64(bl.Height)
	//normTxCount = int64(len(bl.Tx_hashes))

	if regTxCount > 0 {
		// Load from mem existing regTxCount and append new value
		currRegTxCount := Graviton_backend.GetTxCount("registration")
		writeWait, _ := time.ParseDuration("50ms")
		for Graviton_backend.Writing == 1 {
			//log.Printf("[Indexer-indexBlock-regTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
			time.Sleep(writeWait)
		}
		Graviton_backend.Writing = 1
		err := Graviton_backend.StoreTxCount(regTxCount+currRegTxCount, "registration")
		Graviton_backend.Writing = 0
		if err != nil {
			log.Printf("ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currRegTxCount, regTxCount, regTxCount+currRegTxCount)
		}
	}

	if burnTxCount > 0 {
		// Load from mem existing burnTxCount and append new value
		currBurnTxCount := Graviton_backend.GetTxCount("burn")
		writeWait, _ := time.ParseDuration("50ms")
		for Graviton_backend.Writing == 1 {
			//log.Printf("[Indexer-indexBlock-burnTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
			time.Sleep(writeWait)
		}
		Graviton_backend.Writing = 1
		err := Graviton_backend.StoreTxCount(burnTxCount+currBurnTxCount, "burn")
		if err != nil {
			log.Printf("ERROR - Error storing burn tx count. DB '%v' - this block count '%v' - total '%v'", currBurnTxCount, burnTxCount, regTxCount+currBurnTxCount)
		}
		Graviton_backend.Writing = 0
	}

	if normTxCount > 0 {
		/*
			// Test code for finding highest tps block
			var io rpc.GetBlockHeaderByHeight_Result
			var ip = rpc.GetBlockHeaderByTopoHeight_Params{TopoHeight: bl.Height - 1}

			if err = client.RPC.CallResult(context.Background(), "DERO.GetBlockHeaderByTopoHeight", ip, &io); err != nil {
				log.Printf("[getBlockHash] GetBlockHeaderByTopoHeight failed: %v\n", err)
				return err
			} else {
				//log.Printf("[getBlockHash] Retrieved block header from topoheight %v\n", height)
				//mainnet = !info.Testnet // inverse of testnet is mainnet
				//log.Printf("%v\n", io)
			}

			blid := io.Block_Header.Hash

			var io2 rpc.GetBlock_Result
			var ip2 = rpc.GetBlock_Params{Hash: blid}

			if err = client.RPC.CallResult(context.Background(), "DERO.GetBlock", ip2, &io2); err != nil {
				log.Printf("[indexBlock] ERROR - GetBlock failed: %v\n", err)
				return err
			}

			var bl2 block.Block
			var block_bin2 []byte

			block_bin2, _ = hex.DecodeString(io2.Blob)
			bl2.Deserialize(block_bin2)

			prevtimestamp := bl2.Timestamp

			// Load from mem existing normTxCount and append new value
			currNormTxCount := Graviton_backend.GetTxCount("normal")

			//log.Printf("%v / (%v - %v)", normTxCount, int64(bl.Timestamp), int64(prevtimestamp))
			tps := normTxCount / ((int64(bl.Timestamp) - int64(prevtimestamp)) / 1000)

			//err := Graviton_backend.StoreTxCount(normTxCount+currNormTxCount, "normal")
			if tps > currNormTxCount {
				err := Graviton_backend.StoreTxCount(tps, "normal")
				if err != nil {
					log.Printf("ERROR - Error storing normal tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, tps, regTxCount+currNormTxCount)
				}

				err = Graviton_backend.StoreTxCount(blheight, "registration")
				if err != nil {
					log.Printf("ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, normTxCount, regTxCount+currNormTxCount)
				}

				err = Graviton_backend.StoreTxCount((int64(bl.Timestamp) - int64(prevtimestamp)), "burn")
				if err != nil {
					log.Printf("ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, normTxCount, regTxCount+currNormTxCount)
				}
			}
		*/

		// Load from mem existing normTxCount and append new value
		currNormTxCount := Graviton_backend.GetTxCount("normal")
		writeWait, _ := time.ParseDuration("50ms")
		for Graviton_backend.Writing == 1 {
			//log.Printf("[Indexer-indexBlock-normTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
			time.Sleep(writeWait)
		}
		Graviton_backend.Writing = 1
		err := Graviton_backend.StoreTxCount(normTxCount+currNormTxCount, "normal")
		if err != nil {
			log.Printf("ERROR - Error storing normal tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, currNormTxCount, normTxCount+currNormTxCount)
		}
		Graviton_backend.Writing = 0

		// Store all normal TXs that contained a SC transfer
		if len(bl_normtxs) > 0 {
			for i := 0; i < len(bl_normtxs); i++ {

			}
		}
	}

	if len(bl_sctxs) > 0 {
		//log.Printf("Block %v has %v SC tx(s).\n", bl.GetHash(), len(bl_sctxs))

		for i := 0; i < len(bl_sctxs); i++ {
			if bl_sctxs[i].Method == "installsc" {
				var contains bool

				code := fmt.Sprintf("%v", bl_sctxs[i].Sc_args.Value("SC_CODE", "S"))

				// Temporary check - will need something more robust to code compare potentially all except InitializePrivate() with a given template file or other filter inputs.
				//contains := strings.Contains(code, "200 STORE(\"somevar\", 1)")
				if search_filter == "" {
					contains = true
				} else {
					contains = strings.Contains(code, search_filter)
				}

				if !contains {
					// Then reject the validation that this is an installsc action and move on
					log.Printf("SCID %v does not contain the search filter string, moving on.\n", bl_sctxs[i].Scid)
				} else {
					// Gets the SC variables (key/value) at a given topoheight and then stores them
					scVars := indexer.RPC.getSCVariables(bl_sctxs[i].Scid, topoheight)

					if len(scVars) > 0 {
						// Append into db for validated SC
						log.Printf("SCID matches search filter. Adding SCID %v / Signer %v\n", bl_sctxs[i].Scid, bl_sctxs[i].Sender)
						indexer.ValidatedSCs = append(indexer.ValidatedSCs, bl_sctxs[i].Scid)

						writeWait, _ := time.ParseDuration("50ms")
						for Graviton_backend.Writing == 1 {
							//log.Printf("[Indexer-indexBlock-sctxshandle] GravitonDB is writing... sleeping for %v...", writeWait)
							time.Sleep(writeWait)
						}
						Graviton_backend.Writing = 1
						err = Graviton_backend.StoreOwner(bl_sctxs[i].Scid, bl_sctxs[i].Sender)
						if err != nil {
							log.Printf("Error storing owner: %v\n", err)
						}

						err = Graviton_backend.StoreInvokeDetails(bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, topoheight, &bl_sctxs[i])
						if err != nil {
							log.Printf("Err storing invoke details. Err: %v\n", err)
							time.Sleep(5 * time.Second)
							return err
						}

						Graviton_backend.StoreSCIDVariableDetails(bl_sctxs[i].Scid, scVars, topoheight)
						Graviton_backend.StoreSCIDInteractionHeight(bl_sctxs[i].Scid, topoheight)
						Graviton_backend.Writing = 0

						//log.Printf("DEBUG -- SCID: %v ; Sender: %v ; Entrypoint: %v ; topoheight : %v ; info: %v", bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, topoheight, &bl_sctxs[i])
					} else {
						log.Printf("SCID '%v' appears to be invalid.", bl_sctxs[i].Scid)
						writeWait, _ := time.ParseDuration("50ms")
						for Graviton_backend.Writing == 1 {
							//log.Printf("[Indexer-indexBlock-sctxshandle] GravitonDB is writing... sleeping for %v...", writeWait)
							time.Sleep(writeWait)
						}
						Graviton_backend.Writing = 1
						Graviton_backend.StoreInvalidSCIDDeploys(bl_sctxs[i].Scid, bl_sctxs[i].Fees)
						Graviton_backend.Writing = 0
					}
				}
			} else {
				if !scidExist(indexer.ValidatedSCs, bl_sctxs[i].Scid) {

					// Validate SCID is *actually* a valid SCID
					valVars := indexer.RPC.getSCVariables(bl_sctxs[i].Scid, topoheight)

					// By returning valid variables of a given Scid (GetSC --> parse vars), we can conclude it is a valid SCID. Otherwise, skip adding to validated scids
					if len(valVars) > 0 {
						log.Printf("SCID matches search filter. Adding SCID %v / Signer %v\n", bl_sctxs[i].Scid, "")
						indexer.ValidatedSCs = append(indexer.ValidatedSCs, bl_sctxs[i].Scid)

						writeWait, _ := time.ParseDuration("50ms")
						for Graviton_backend.Writing == 1 {
							//log.Printf("[Indexer-indexBlock-sctxshandle] GravitonDB is writing... sleeping for %v...", writeWait)
							time.Sleep(writeWait)
						}
						Graviton_backend.Writing = 1
						err = Graviton_backend.StoreOwner(bl_sctxs[i].Scid, "")
						if err != nil {
							log.Printf("Error storing owner: %v\n", err)
						}
						Graviton_backend.Writing = 0
					}
				}

				if scidExist(indexer.ValidatedSCs, bl_sctxs[i].Scid) {
					//log.Printf("SCID %v is validated, checking the SC TX entrypoints to see if they should be logged.\n", bl_sctxs[i].Scid)
					// TODO: Modify this to be either all entrypoints, just Start, or a subset that is defined in pre-run params or not needed?
					//if bl_sctxs[i].entrypoint == "Start" {
					//if bl_sctxs[i].Entrypoint == "InputStr" {
					if true {
						currsctx := bl_sctxs[i]

						//log.Printf("Tx %v matches scinvoke call filter(s). Adding %v to DB.\n", bl_sctxs[i].Txid, currsctx)

						writeWait, _ := time.ParseDuration("50ms")
						for Graviton_backend.Writing == 1 {
							//log.Printf("[Indexer-indexBlock-sctxshandle] GravitonDB is writing... sleeping for %v...", writeWait)
							time.Sleep(writeWait)
						}
						Graviton_backend.Writing = 1
						err = Graviton_backend.StoreInvokeDetails(bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, topoheight, &currsctx)
						if err != nil {
							log.Printf("Err storing invoke details. Err: %v\n", err)
							time.Sleep(5 * time.Second)
							return err
						}

						// Gets the SC variables (key/value) at a given topoheight and then stores them
						scVars := indexer.RPC.getSCVariables(bl_sctxs[i].Scid, topoheight)
						Graviton_backend.StoreSCIDVariableDetails(bl_sctxs[i].Scid, scVars, topoheight)
						Graviton_backend.StoreSCIDInteractionHeight(bl_sctxs[i].Scid, topoheight)
						Graviton_backend.Writing = 0

						//log.Printf("DEBUG -- SCID: %v ; Sender: %v ; Entrypoint: %v ; topoheight : %v ; info: %v", bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, topoheight, &currsctx)
					} else {
						//log.Printf("Tx %v does not match scinvoke call filter(s), but %v instead. This should not (currently) be added to DB.\n", bl_sctxs[i].Txid, bl_sctxs[i].Entrypoint)
					}
				} else {
					//log.Printf("SCID %v is not validated and thus we do not log SC interactions for this. Moving on.\n", bl_sctxs[i].Scid)
				}
			}
		}
	} else {
		//log.Printf("Block %v does not have any SC txs\n", bl.GetHash())
	}

	return err
}

// Check if value exists within a string array/slice
func scidExist(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

// DERO.GetBlockHeaderByTopoHeight rpc call for returning block hash at a particular topoheight
func (client *Client) getBlockHash(height uint64) (hash string, err error) {
	//log.Printf("[getBlockHash] Attempting to get block details at topoheight %v\n", height)

	var io rpc.GetBlockHeaderByHeight_Result
	var ip = rpc.GetBlockHeaderByTopoHeight_Params{TopoHeight: height}

	if err = client.RPC.CallResult(context.Background(), "DERO.GetBlockHeaderByTopoHeight", ip, &io); err != nil {
		//log.Printf("[getBlockHash] GetBlockHeaderByTopoHeight failed: %v\n", err)
		return hash, fmt.Errorf("GetBlockHeaderByTopoHeight failed: %v\n", err)
	} else {
		//log.Printf("[getBlockHash] Retrieved block header from topoheight %v\n", height)
		//mainnet = !info.Testnet // inverse of testnet is mainnet
		//log.Printf("%v\n", io)
	}

	hash = io.Block_Header.Hash

	return hash, err
}

// Looped interval to probe DERO.GetInfo rpc call for updating chain topoheight
func (indexer *Indexer) getInfo() {
	var reconnect_count int
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		var err error

		var info *structures.GetInfo

		// collect all the data afresh,  execute rpc to service
		if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetInfo", nil, &info); err != nil {
			//log.Printf("[getInfo] ERROR - GetInfo failed: %v\n", err)

			// TODO: Perhaps just a .Closing = true call here and then gnomonserver can be polling for any indexers with .Closing then close the rest cleanly. If packaged, then just have to handle themselves w/ .Close()
			if reconnect_count >= 5 && indexer.CloseOnDisconnect {
				indexer.Close()
				break
			}
			time.Sleep(1 * time.Second)
			indexer.RPC.Connect(indexer.Endpoint) // Attempt to re-connect now

			reconnect_count++

			continue
		} else {
			if reconnect_count > 0 {
				reconnect_count = 0
			}
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//log.Printf("%v\n", info)
		}

		currStoreGetInfo := indexer.Backend.GetGetInfoDetails()
		if currStoreGetInfo != nil {
			if currStoreGetInfo.Height < info.Height {
				var structureGetInfo *structures.GetInfo
				structureGetInfo = info
				writeWait, _ := time.ParseDuration("50ms")
				for indexer.Backend.Writing == 1 {
					//log.Printf("[Indexer-getInfo] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}
				indexer.Backend.Writing = 1
				err := indexer.Backend.StoreGetInfoDetails(structureGetInfo)
				if err != nil {
					log.Printf("[getInfo] ERROR - GetInfo store failed: %v\n", err)
				}
				indexer.Backend.Writing = 0
			}
		} else {
			var structureGetInfo *structures.GetInfo
			structureGetInfo = info
			writeWait, _ := time.ParseDuration("50ms")
			for indexer.Backend.Writing == 1 {
				//log.Printf("[Indexer-getInfo] GravitonDB is writing... sleeping for %v...", writeWait)
				time.Sleep(writeWait)
			}
			indexer.Backend.Writing = 1
			err := indexer.Backend.StoreGetInfoDetails(structureGetInfo)
			if err != nil {
				log.Printf("[getInfo] ERROR - GetInfo store failed: %v\n", err)
			}
			indexer.Backend.Writing = 0
		}

		indexer.ChainHeight = info.TopoHeight

		time.Sleep(5 * time.Second)
	}
}

// Looped interval to probe WALLET.GetHeight rpc call for updating wallet height
func (indexer *Indexer) getWalletHeight() {
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		var err error

		var info rpc.GetHeight_Result

		// collect all the data afresh,  execute rpc to service
		if err = indexer.RPC.RPC.CallResult(context.Background(), "WALLET.GetHeight", nil, &info); err != nil {
			log.Printf("[getInfo] ERROR - GetHeight failed: %v\n", err)
			time.Sleep(1 * time.Second)
			indexer.RPC.Connect(indexer.Endpoint) // Attempt to re-connect now
			continue
		} else {
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//log.Printf("%v\n", info)
		}

		indexer.ChainHeight = int64(info.Height)

		time.Sleep(5 * time.Second)
	}
}

// Gets SC variable details
func (client *Client) getSCVariables(scid string, topoheight int64) []*structures.SCIDVariable {
	var err error
	var variables []*structures.SCIDVariable

	var getSCResults rpc.GetSC_Result
	getSCParams := rpc.GetSC_Params{SCID: scid, Code: false, Variables: true, TopoHeight: topoheight}
	if err = client.RPC.CallResult(context.Background(), "DERO.GetSC", getSCParams, &getSCResults); err != nil {
		log.Printf("[getSCVariables] ERROR - getSCVariables failed: %v\n", err)
		return variables
	}

	for k, v := range getSCResults.VariableStringKeys {
		currVar := &structures.SCIDVariable{}
		// We don't currently need to store "C" through these means.
		if k == "C" {
			continue
		}
		currVar.Key = k
		switch cval := v.(type) {
		case uint64:
			currVar.Value = cval
		case string:
			// hex decode since all strings are hex encoded
			dstr, _ := hex.DecodeString(cval)
			p := new(crypto.Point)
			if err := p.DecodeCompressed(dstr); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				currVar.Value = string(dstr)
			}
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			currVar.Value = str
		}
		variables = append(variables, currVar)
	}

	for k, v := range getSCResults.VariableUint64Keys {
		currVar := &structures.SCIDVariable{}
		currVar.Key = k
		switch cval := v.(type) {
		case string:
			// hex decode since all strings are hex encoded
			decd, _ := hex.DecodeString(cval)
			p := new(crypto.Point)
			if err := p.DecodeCompressed(decd); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				currVar.Value = string(decd)
			}
		case uint64:
			currVar.Value = cval
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			currVar.Value = str
		}
		variables = append(variables, currVar)
	}

	return variables
}

// Close cleanly the indexer
func (ind *Indexer) Close() {
	// Tell indexer a closing operation is happening; this will close out loops on next iteration
	ind.Closing = true

	// Sleep for safety
	time.Sleep(time.Second * 5)

	// Close websocket connection cleanly
	ind.RPC.WS.Close()

	// Close out grav db cleanly
	ind.Backend.DB.Close()
}
