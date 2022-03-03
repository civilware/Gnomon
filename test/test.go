package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"time"
	"unicode"
	"unsafe"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"

	"github.com/ybbus/jsonrpc"
)

// This is for testing and is inconsistent/unreliable code. Not intended to be used for anything other than poking at different calls and details.
func main() {
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
