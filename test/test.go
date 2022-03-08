package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strconv"
	"time"
	"unicode"
	"unsafe"

	"github.com/civilware/Gnomon/rwc"
	"github.com/civilware/Gnomon/structures"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/deroproject/derohe/rpc"
	"github.com/gorilla/websocket"
)

type Client struct {
	WS  *websocket.Conn
	RPC *jrpc2.Client
}

var Connected bool

// This is for testing and is inconsistent/unreliable code. Not intended to be used for anything other than poking at different calls and details.
func main() {
	var err error

	RPC := &Client{}
	for {
		log.Printf("Trying to connect...")
		err = RPC.Connect("127.0.0.1:10102")
		if err != nil {
			continue
		}
		break
	}
	//}()
	time.Sleep(1 * time.Second)

	vars := RPC.getSCVariables("0000000000000000000000000000000000000000000000000000000000000001", 58)

	for _, v := range vars {
		log.Printf("K: %v ; V: %v", v.Key, v.Value)
	}

	/*
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
