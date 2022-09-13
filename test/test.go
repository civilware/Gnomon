package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unsafe"

	"github.com/civilware/Gnomon/rwc"
	"github.com/civilware/Gnomon/structures"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/deroproject/graviton"
	"github.com/gorilla/websocket"
)

type Client struct {
	WS  *websocket.Conn
	RPC *jrpc2.Client
}

type Derodbstore struct {
	Balance_store  *graviton.Store // stores most critical data, only history can be purged, its merkle tree is stored in the block
	Block_tx_store Storefs         // stores blocks which can be discarded at any time(only past but keep recent history for rollback)
	Topo_store     Storetopofs     // stores topomapping which can only be discarded by punching holes in the start of the file
}

type Storefs struct {
	Basedir string
}

type Storetopofs struct {
	Topomapping *os.File
}

var Connected bool
var DeroDB = &Derodbstore{}

// This is for testing and is inconsistent/unreliable code. Not intended to be used for anything other than poking at different calls and details.
func main() {
	log.Printf("Hello World")

	//var blid = "256480179e6e02cf386fbf1d61a5401bf61998eb1e8ea72dd46054b10a7be972"
	var err error

	var client = &Client{}
	var endpoint = "127.0.0.1:40402"

	client.WS, _, err = websocket.DefaultDialer.Dial("ws://"+endpoint+"/ws", nil)
	input_output := rwc.New(client.WS)
	client.RPC = jrpc2.NewClient(channel.RawJSON(input_output, input_output), nil)

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
		return
	}

	var inputparam rpc.GetTransaction_Params
	var output rpc.GetTransaction_Result

	//inputparam.Tx_Hashes = append(inputparam.Tx_Hashes, "015d4406b3f96ff38d63192cbffa39014e5c54cdb3f9bb8eb937f5183ee2257a")
	inputparam.Tx_Hashes = append(inputparam.Tx_Hashes, "daacbf8150162b786b07b6d2331c8be29e69a9d33044bfa756227a264f88a9db")

	if err = client.RPC.CallResult(context.Background(), "DERO.GetTransaction", inputparam, &output); err != nil {
		log.Printf("[indexBlock] ERROR - GetTransaction for txid '%v' failed: %v\n", inputparam.Tx_Hashes, err)
		return
	}

	tx_bin, _ := hex.DecodeString(output.Txs_as_hex[0])
	var tx transaction.Transaction
	tx.Deserialize(tx_bin)

	log.Printf("%v", tx.TransactionType)
	log.Printf("%v", tx)

	/*
		var io rpc.GetBlock_Result
		var ip = rpc.GetBlock_Params{Hash: blid}

		if err = client.RPC.CallResult(context.Background(), "DERO.GetBlock", ip, &io); err != nil {
			log.Printf("[indexBlock] ERROR - GetBlock failed: %v\n", err)
			return
		}

		// Gets SC variable details
		var variables []*structures.SCIDVariable

		var getSCResults rpc.GetSC_Result
		getSCParams := rpc.GetSC_Params{SCID: "ae55db1581b79f02f86b70fc338a7b91b14ded071a31972d9cfdb0eca6e302af", Code: false, Variables: true, TopoHeight: 529769}
		if err = client.RPC.CallResult(context.Background(), "DERO.GetSC", getSCParams, &getSCResults); err != nil {
			log.Printf("[getSCVariables] ERROR - getSCVariables failed: %v\n", err)
			return
		}

		for k, v := range getSCResults.VariableStringKeys {
			currVar := &structures.SCIDVariable{}
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
			//return
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
			//return
		}
	*/

	/*
		// Height sorting/testing
		var heights []int64
		heights = append(heights, int64(32))
		heights = append(heights, int64(21))
		heights = append(heights, int64(56))
		heights = append(heights, int64(87))
		heights = append(heights, int64(2))

		test := int64(1)

		sort.SliceStable(heights, func(i, j int) bool {
			return heights[i] > heights[j]
		})

		log.Printf("%v", heights)

		if test > heights[0] {
			log.Printf("Value larger than interacted heights defined. Returning latest height values - %v", heights[0])
			return
		}

		for i := 1; i < len(heights); i++ {
			if heights[i] < test {
				return heights[i]
			} else if heights[i] == test {
				return heights[i]
			}
		}

		log.Printf("Value lower than all interacted heights - return nil?")
	*/

	/*
		// Wallet mode testing stuff....
			var walletRPCClient jsonrpc.RPCClient
			walletEndpoint := "127.0.0.1:40403"
			walletRPCClient = jsonrpc.NewClient("http://" + walletEndpoint + "/json_rpc")
			var t crypto.Hash
			str := []byte("80af6dd3e0af20530b108efd621b8053e066c7895491c0838a888f57599f8ee6")
			//copy(t, str[0:32])
			_ = str
			//DEST_PORT := uint64(0x1234567812345678)

			time.Sleep(time.Second)

			var transfers rpc.Get_Transfers_Result
			err := walletRPCClient.CallFor(&transfers, "GetTransfers", rpc.Get_Transfers_Params{SCID: t, Coinbase: true, In: true, Out: true, Min_Height: 2506, Max_Height: 2506, Sender: "", Receiver: "", DestinationPort: 0, SourcePort: 0}) //, SourcePort: DEST_PORT, DestinationPort: DEST_PORT})
			if err != nil {
				log.Printf("[processReceivedMessages] Could not obtain gettransfers from wallet err %s\n", err)
			}

			for _, e := range transfers.Entries {
				args, _ := e.ProcessPayload()
				_ = args
				log.Printf("%v", e)
			}

			var daemonRPCClient jsonrpc.RPCClient
			daemonEndpoint := "127.0.0.1:40402"
			daemonRPCClient = jsonrpc.NewClient("http://" + daemonEndpoint + "/json_rpc")

			var tx transaction.Transaction
			scidaddr := "217cc438d6f205c3600d6e71cdcabe3561eb50f189971b49189dc25ec35eb46f"

			var inputparam rpc.GetTransaction_Params
			var output rpc.GetTransaction_Result

			inputparam.Tx_Hashes = append(inputparam.Tx_Hashes, scidaddr)

			err = daemonRPCClient.CallFor(&output, "gettransactions", inputparam)
			if err != nil {
				log.Printf("Err daemon: %v", err)
			}

			tx_bin, _ := hex.DecodeString(output.Txs_as_hex[0])
			tx.Deserialize(tx_bin)

			var buf []byte
			r := bytes.NewReader(buf[:])
			tx.Payloads[0].UnmarshalProofs(r)
			tx.Payloads[0].UnmarshalHeaderStatement(r)
			log.Printf("%v", tx.Payloads[0].Statement)
			log.Printf("%v", tx.Payloads[0].SCID)
			log.Printf("%v", tx.Payloads[0].Statement.RingSize)
			echanges := crypto.ConstructElGamal(tx.Payloads[0].Statement.C[0], tx.Payloads[0].Statement.D)
			log.Printf("%v", echanges)
	*/

}

func (client *Client) getSCVariables(scid string, topoheight int64) []*structures.SCIDVariable {
	var err error
	var variables []*structures.SCIDVariable

	var getSCResults rpc.GetSC_Result
	getSCParams := rpc.GetSC_Params{SCID: scid, Code: false, Variables: true, TopoHeight: topoheight}
	if err = client.RPC.CallResult(context.Background(), "DERO.GetSC", getSCParams, &getSCResults); err != nil {
		log.Printf("[indexBlock] ERROR - GetBlock failed: %v\n", err)
		return variables
	}

	for k, v := range getSCResults.VariableStringKeys {
		currVar := &structures.SCIDVariable{}
		currVar.Key = k
		switch cval := v.(type) {
		case string:
			currVar.Value = cval
		default:
			str := fmt.Sprintf("%v", cval)
			currVar.Value = str
		}
		//currVar.Value = v.(string)
		variables = append(variables, currVar)
	}

	return variables
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

type BytesViewer []byte

// String returns view in hexadecimal
func (b BytesViewer) String() string {
	if len(b) == 0 {
		return "invlaid string"
	}
	const head = `
| Address  | Hex                                             | Text             |
| -------: | :---------------------------------------------- | :--------------- |
`
	const row = 16
	result := make([]byte, 0, len(head)/2*(len(b)/16+3))
	result = append(result, head...)
	for i := 0; i < len(b); i += row {
		result = append(result, "| "...)
		result = append(result, fmt.Sprintf("%08x", i)...)
		result = append(result, " | "...)

		k := i + row
		more := 0
		if k >= len(b) {
			more = k - len(b)
			k = len(b)
		}
		for j := i; j != k; j++ {
			if b[j] < 16 {
				result = append(result, '0')
			}
			result = strconv.AppendUint(result, uint64(b[j]), 16)
			result = append(result, ' ')
		}
		for j := 0; j != more; j++ {
			result = append(result, "   "...)
		}
		result = append(result, "| "...)
		buf := bytes.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return ' '
			}
			return r
		}, b[i:k])
		result = append(result, buf...)
		for j := 0; j != more; j++ {
			result = append(result, ' ')
		}
		result = append(result, " |\n"...)
	}
	return *(*string)(unsafe.Pointer(&result))
}

// ---- DERO DB functions ---- //
func (s *Derodbstore) LoadDeroDB() (err error) {
	// Temp defining for now to same directory as testnet folder - TODO: see if we can natively pull in storage location? I doubt it..
	current_path, err := os.Getwd()
	current_path = filepath.Join(current_path, "testnet")

	current_path = filepath.Join(current_path, "balances")

	if s.Balance_store, err = graviton.NewDiskStore(current_path); err == nil {
		if err = s.Topo_store.Open(current_path); err == nil {
			s.Block_tx_store.Basedir = current_path
		}
	}

	if err != nil {
		log.Printf("Err - Cannot open store: %v\n", err)
		return err
	}
	log.Printf("Initialized: %v\n", current_path)

	return nil
}

func (s *Storetopofs) Open(basedir string) (err error) {
	s.Topomapping, err = os.OpenFile(filepath.Join(basedir, "topo.map"), os.O_RDWR|os.O_CREATE, 0700)
	return err
}

// Different storefs pointers, thus used exported function within. Reference: https://github.com/deroproject/derohe/blob/main/blockchain/storefs.go#L124
func (s *Storefs) ReadBlockSnapshotVersion(h [32]byte) (uint64, error) {
	dir := filepath.Join(filepath.Join(s.Basedir, "bltx_store"), fmt.Sprintf("%02x", h[0]), fmt.Sprintf("%02x", h[1]), fmt.Sprintf("%02x", h[2]))

	files, err := os.ReadDir(dir) // this always returns the sorted list
	if err != nil {
		return 0, err
	}
	// windows has a caching issue, so earlier versions may exist at the same time
	// so we mitigate it, by using the last version, below 3 lines reverse the already sorted arrray
	for left, right := 0, len(files)-1; left < right; left, right = left+1, right-1 {
		files[left], files[right] = files[right], files[left]
	}

	filename_start := fmt.Sprintf("%x.block", h[:])
	for _, file := range files {
		if strings.HasPrefix(file.Name(), filename_start) {
			var ssversion uint64
			parts := strings.Split(file.Name(), "_")
			if len(parts) != 4 {
				panic("such filename cannot occur")
			}
			_, err := fmt.Sscan(parts[2], &ssversion)
			if err != nil {
				return 0, err
			}
			return ssversion, nil
		}
	}

	return 0, os.ErrNotExist
}

// ---- End DERO DB functions ---- //
