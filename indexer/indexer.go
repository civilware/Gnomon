package indexer

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/civilware/Gnomon/rwc"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/cryptography/bn256"
	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/deroproject/graviton"

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
	ChainTopoHeight   int64
	SearchFilter      string
	Backend           *storage.GravitonStore
	Closing           bool
	RPC               *Client
	DaemonEndpoint    string
}

var daemon_endpoint string
var Connected bool = false
var validated_scs []string
var chain_topoheight int64

var DeroDB *storage.Derodbstore = &storage.Derodbstore{}

func NewIndexer(Graviton_backend *storage.GravitonStore, search_filter string, last_indexedheight int64, daemon_endpoint string) *Indexer {
	var err error

	// TODO: Dynamically get SCIDs of hardcoded SCs and append them
	validated_scs = append(validated_scs, "0000000000000000000000000000000000000000000000000000000000000001")
	err = Graviton_backend.StoreOwner("0000000000000000000000000000000000000000000000000000000000000001", "")
	if err != nil {
		log.Printf("Error storing owner: %v\n", err)
	}

	storedindex := Graviton_backend.GetLastIndexHeight()
	if storedindex > last_indexedheight {
		log.Printf("[Main] Continuing from last indexed height %v\n", storedindex)
		last_indexedheight = storedindex

		// We can also assume this check to mean we have stored validated SCs potentially. TODO: Do we just get stored SCs regardless of sync cycle?
		//pre_validatedSCIDs := make(map[string]string)
		pre_validatedSCIDs := Graviton_backend.GetAllOwnersAndSCIDs()

		if len(pre_validatedSCIDs) > 0 {
			log.Printf("[Main] Appending pre-validated SCIDs from store to memory.\n")

			for k := range pre_validatedSCIDs {
				validated_scs = append(validated_scs, k)
			}
		}
	}

	return &Indexer{
		LastIndexedHeight: last_indexedheight,
		SearchFilter:      search_filter,
		Backend:           Graviton_backend,
		RPC:               &Client{},
		DaemonEndpoint:    daemon_endpoint,
	}
}

func (indexer *Indexer) Start() {
	var err error

	// Simple connect loop .. if connection fails initially then keep trying, else break out and continue on. Connect() is handled in getInfo() for retries later on if connection ceases again
	go func() {
		for {
			err = indexer.RPC.Connect(indexer.DaemonEndpoint)
			if err != nil {
				continue
			}
			break
		}
	}()
	time.Sleep(1 * time.Second)

	// Continuously getInfo from daemon to update topoheight globally
	go indexer.getInfo()
	time.Sleep(1 * time.Second)

	go func() {
		for {
			if indexer.Closing {
				// Break out on closing call
				break
			}

			if indexer.LastIndexedHeight > indexer.ChainTopoHeight {
				time.Sleep(1 * time.Second)
				continue
			}

			blid, err := indexer.RPC.getBlockHash(uint64(indexer.LastIndexedHeight))
			if err != nil {
				log.Printf("[mainFOR] ERROR - %v\n", err)
				time.Sleep(1 * time.Second)
				continue
			}

			err = indexer.RPC.indexBlock(blid, indexer.LastIndexedHeight, indexer.SearchFilter, indexer.Backend)
			if err != nil {
				log.Printf("[mainFOR] ERROR - %v\n", err)
				time.Sleep(time.Second)
				continue
			}

			indexer.LastIndexedHeight++
		}
	}()

	// Hold
	select {}
}

func (client *Client) Connect(daemon_endpoint string) (err error) {
	client.WS, _, err = websocket.DefaultDialer.Dial("ws://"+daemon_endpoint+"/ws", nil)

	// notify user of any state change
	// if daemon connection breaks or comes live again
	if err == nil {
		if !Connected {
			log.Printf("[Connect] Connection to RPC server successful - ws://%s/ws\n", daemon_endpoint)
			Connected = true
		}
	} else {
		log.Printf("[Connect] ERROR connecting to daemon %v\n", err)

		if Connected {
			log.Printf("[Connect] ERROR - Connection to RPC server Failed - ws://%s/ws\n", daemon_endpoint)
		}
		Connected = false
		return err
	}

	input_output := rwc.New(client.WS)
	client.RPC = jrpc2.NewClient(channel.RawJSON(input_output, input_output), nil)

	return err
}

func (client *Client) indexBlock(blid string, topoheight int64, search_filter string, Graviton_backend *storage.GravitonStore) (err error) {
	var io rpc.GetBlock_Result
	var ip = rpc.GetBlock_Params{Hash: blid}

	if err = client.RPC.CallResult(context.Background(), "DERO.GetBlock", ip, &io); err != nil {
		log.Printf("[indexBlock] ERROR - GetBlock failed: %v\n", err)
		return err
	}

	var bl block.Block
	var block_bin []byte

	block_bin, _ = hex.DecodeString(io.Blob)
	bl.Deserialize(block_bin)

	var bl_sctxs []structures.Parse

	for i := 0; i < len(bl.Tx_hashes); i++ {
		var tx transaction.Transaction
		var sc_args rpc.Arguments
		var sender string

		var inputparam rpc.GetTransaction_Params
		var output rpc.GetTransaction_Result

		inputparam.Tx_Hashes = append(inputparam.Tx_Hashes, bl.Tx_hashes[i].String())

		if err = client.RPC.CallResult(context.Background(), "DERO.GetTransaction", inputparam, &output); err != nil {
			log.Printf("[indexBlock] ERROR - GetTransaction failed: %v\n", err)
			return err
		}

		tx_bin, _ := hex.DecodeString(output.Txs_as_hex[0])
		tx.Deserialize(tx_bin)

		if tx.TransactionType == transaction.SC_TX {
			sc_args = tx.SCDATA
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

			if tx.Payloads[0].Statement.RingSize == 2 {
				sender = output.Txs[0].Signer
				/*
					if sender == "" {
						sender, err = getTxSender(tx)
						if err != nil {
							log.Printf("ERR - Error getting tx sender - %v\n", err)
							return err
						}
						log.Printf("TEMP - Received sender from getTxSender(tx).\n")
					} else {
						log.Printf("TEMP - Received sender from output.Txs[0].Signer\n")
					}
				*/
			} else {
				log.Printf("ERR - Ringsize for %v is != 2. Storing blank value for txid sender.\n", bl.Tx_hashes[i])
				//continue
				if method == "installsc" {
					// We do not store a ringsize > 2 of installsc calls. Only of SC interactions via sc_invoke for ringsize > 2 and just blank out the sender
					continue
				}
			}
			//time.Sleep(2 * time.Second)
			bl_sctxs = append(bl_sctxs, structures.Parse{Txid: bl.Tx_hashes[i].String(), Scid: scid, Scid_hex: scid_hex, Entrypoint: entrypoint, Method: method, Sc_args: sc_args, Sender: sender})
		} else {
			//log.Printf("TX %v is NOT a SC transaction.\n", bl.Tx_hashes[i])
		}
	}

	if len(bl_sctxs) > 0 {
		log.Printf("Block %v has %v SC tx(s).\n", bl.GetHash(), len(bl_sctxs))

		for i := 0; i < len(bl_sctxs); i++ {
			if bl_sctxs[i].Method == "installsc" {
				var contains bool

				code := fmt.Sprintf("%v", bl_sctxs[i].Sc_args.Value("SC_CODE", "S"))

				// Temporary check - will need something more robust to code compare potentially all except InitializePrivate() with the template file.
				//contains := strings.Contains(code, "200 STORE(\"artificerfee\", 1)")
				if search_filter == "" {
					contains = true
				} else {
					contains = strings.Contains(code, search_filter)
				}

				if !contains {
					// Then reject the validation that this is an artificer installsc action and move on
					log.Printf("SCID %v does not contain the search filter string, moving on.\n", bl_sctxs[i].Scid)
				} else {
					// Append into db for artificer validated SC
					log.Printf("SCID matches search filter. Adding SCID %v / Signer %v\n", bl_sctxs[i].Scid, bl_sctxs[i].Sender)
					validated_scs = append(validated_scs, bl_sctxs[i].Scid)

					err = Graviton_backend.StoreOwner(bl_sctxs[i].Scid, bl_sctxs[i].Sender)
					if err != nil {
						log.Printf("Error storing owner: %v\n", err)
					}

					//owner := Graviton_backend.GetOwner(bl_sctxs[i].Scid)
					//log.Printf("Owner of %v is %v", bl_sctxs[i].Scid, owner)
				}
			} else {
				if scidExist(validated_scs, bl_sctxs[i].Scid) {
					log.Printf("SCID %v is validated, checking the SC TX entrypoints to see if they should be logged.\n", bl_sctxs[i].Scid)
					// TODO: Modify this to be either all entrypoints, just Start, or a subset that is defined in pre-run params
					//if bl_sctxs[i].entrypoint == "Start" {
					//if bl_sctxs[i].Entrypoint == "InputStr" {
					if true {
						currsctx := bl_sctxs[i]

						log.Printf("Tx %v matches scinvoke call filter(s). Adding %v to DB.\n", bl_sctxs[i].Txid, currsctx)

						err = Graviton_backend.StoreInvokeDetails(bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, topoheight, &currsctx)
						if err != nil {
							log.Printf("Err storing invoke details. Err: %v\n", err)
							time.Sleep(5 * time.Second)
							return err
						}
					} else {
						log.Printf("Tx %v does not match scinvoke call filter(s), but %v instead. This should not (currently) be added to DB.\n", bl_sctxs[i].Txid, bl_sctxs[i].Entrypoint)
					}
				} else {
					log.Printf("SCID %v is not validated and thus we do not log SC interactions for this. Moving on.\n", bl_sctxs[i].Scid)
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

// Uses a transaction to find the tx sender/signer of a ringsize 2 SC transaction by querying DERO Chain DB
func getTxSender(tx transaction.Transaction) (string, error) {

	// ----- Start publickeylist expansion ----- //
	// Didn't use all code, but could add more checks if req'd. Unexportable func also we don't want any balance writes etc. (not that they'd be accepted)
	// Reference: https://github.com/deroproject/derohe/blob/main/blockchain/transaction_verify.go#L336

	for t := range tx.Payloads {
		key_map := map[string]bool{}
		for i := 0; i < int(tx.Payloads[t].Statement.RingSize); i++ {
			key_map[string(tx.Payloads[t].Statement.Publickeylist_pointers[i*int(tx.Payloads[t].Statement.Bytes_per_publickey):(i+1)*int(tx.Payloads[t].Statement.Bytes_per_publickey)])] = true
		}
		if len(key_map) != int(tx.Payloads[t].Statement.RingSize) {
			return "", fmt.Errorf("key_map does not contain ringsize members, ringsize %d , bytesperkey %d data %x", tx.Payloads[t].Statement.RingSize, tx.Payloads[t].Statement.Bytes_per_publickey, tx.Payloads[t].Statement.Publickeylist_pointers[:])
		}
		tx.Payloads[t].Statement.CLn = tx.Payloads[t].Statement.CLn[:0]
		tx.Payloads[t].Statement.CRn = tx.Payloads[t].Statement.CRn[:0]
		//log.Printf("Key Map (make sure all are true): %v\n", key_map)
	}

	// transaction needs to be expanded. this expansion needs balance state
	version, err := DeroDB.Block_tx_store.ReadBlockSnapshotVersion(tx.BLID)

	ss, err := DeroDB.Balance_store.LoadSnapshot(version)

	var balance_tree *graviton.Tree
	if balance_tree, err = ss.GetTree("B"); err != nil {
		return "", err
	}

	if balance_tree == nil {
		return "", fmt.Errorf("mentioned balance tree not found, cannot verify TX\n")
	}

	trees := map[crypto.Hash]*graviton.Tree{}

	var zerohash crypto.Hash
	trees[zerohash] = balance_tree // initialize main tree by default

	for t := range tx.Payloads {
		tx.Payloads[t].Statement.Publickeylist_compressed = tx.Payloads[t].Statement.Publickeylist_compressed[:0]
		tx.Payloads[t].Statement.Publickeylist = tx.Payloads[t].Statement.Publickeylist[:0]

		//log.Printf("Tree: %v\n", balance_tree)

		var tree *graviton.Tree

		if _, ok := trees[tx.Payloads[t].SCID]; ok {
			tree = trees[tx.Payloads[t].SCID]
		} else {

			//	fmt.Printf("SCID loading %s tree\n", tx.Payloads[t].SCID)
			tree, _ = ss.GetTree(string(tx.Payloads[t].SCID[:]))
			trees[tx.Payloads[t].SCID] = tree
		}

		// now lets calculate CLn and CRn
		for i := 0; i < int(tx.Payloads[t].Statement.RingSize); i++ {
			key_pointer := tx.Payloads[t].Statement.Publickeylist_pointers[i*int(tx.Payloads[t].Statement.Bytes_per_publickey) : (i+1)*int(tx.Payloads[t].Statement.Bytes_per_publickey)]
			_, key_compressed, _, err := tree.GetKeyValueFromHash(key_pointer)

			// if destination address could be found be found in sc balance tree, assume its zero balance
			if err != nil && !tx.Payloads[t].SCID.IsZero() {
				if xerrors.Is(err, graviton.ErrNotFound) { // if the address is not found, lookup in main tree
					_, key_compressed, _, err = balance_tree.GetKeyValueFromHash(key_pointer)
					if err != nil {
						return "", fmt.Errorf("Publickey not obtained. Are you connected to the daemon db? err %s\n", err)
					}
				}
			}
			if err != nil {
				return "", fmt.Errorf("Publickey not obtained. Are you connected to the daemon db? err %s\n", err)
			}

			// decode public key and expand
			{
				var p bn256.G1
				var pcopy [33]byte
				copy(pcopy[:], key_compressed)
				if err = p.DecodeCompressed(key_compressed[:]); err != nil {
					return "", fmt.Errorf("key %d could not be decompressed", i)
				}
				tx.Payloads[t].Statement.Publickeylist_compressed = append(tx.Payloads[t].Statement.Publickeylist_compressed, pcopy)
				tx.Payloads[t].Statement.Publickeylist = append(tx.Payloads[t].Statement.Publickeylist, &p)
			}
		}
	}

	var signer [33]byte

	for t := range tx.Payloads {
		if uint64(len(tx.Payloads[t].Statement.Publickeylist_compressed)) != tx.Payloads[t].Statement.RingSize {
			panic("tx is not expanded")
		}
		if tx.Payloads[t].SCID.IsZero() && tx.Payloads[t].Statement.RingSize == 2 {
			parity := tx.Payloads[t].Proof.Parity()
			for i := 0; i < int(tx.Payloads[t].Statement.RingSize); i++ {
				if (i%2 == 0) == parity { // this condition is well thought out and works good enough
					copy(signer[:], tx.Payloads[t].Statement.Publickeylist_compressed[i][:])
				}
			}

		}
	}

	address := &rpc.Address{
		Mainnet:   false,
		PublicKey: new(crypto.Point),
	}

	err = address.PublicKey.DecodeCompressed(signer[0:33])

	if err != nil {
		return "", fmt.Errorf("Signer decodecompressed issues. err %s\n", err)
	}

	//log.Printf("Signer is: %v\n", address.String())

	return address.String(), err

	// ----- End publickeylist expansion ----- //
}

// DERO.GetBlockHeaderByTopoHeight rpc call for returning block hash at a particular topoheight
func (client *Client) getBlockHash(height uint64) (hash string, err error) {
	//log.Printf("[getBlockHash] Attempting to get block details at topoheight %v\n", height)

	var io rpc.GetBlockHeaderByHeight_Result
	var ip = rpc.GetBlockHeaderByTopoHeight_Params{TopoHeight: height}

	if err = client.RPC.CallResult(context.Background(), "DERO.GetBlockHeaderByTopoHeight", ip, &io); err != nil {
		log.Printf("[getBlockHash] GetBlockHeaderByTopoHeight failed: %v\n", err)
		return hash, err
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
	for {
		var err error

		var info rpc.GetInfo_Result

		// collect all the data afresh,  execute rpc to service
		if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetInfo", nil, &info); err != nil {
			log.Printf("[getInfo] ERROR - GetInfo failed: %v\n", err)
			time.Sleep(1 * time.Second)
			indexer.RPC.Connect(indexer.DaemonEndpoint) // Attempt to re-connect now
			continue
		} else {
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//log.Printf("%v\n", info)
		}

		indexer.ChainTopoHeight = info.TopoHeight

		time.Sleep(5 * time.Second)
	}
}
