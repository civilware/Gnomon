package structures

import (
	"encoding/json"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/sirupsen/logrus"
)

// global logger
var Logger logrus.Logger

// After daemon connection will check if mainnet/testnet and adjust accordingly
const MAINNET_GNOMON_SCID = "a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4"
const TESTNET_GNOMON_SCID = "c9d23d2fc3aaa8e54e238a2218c0e5176a6e48780920fd8474fac5b0576110a2"

type SCTXParse struct {
	Txid       string
	Scid       string
	Scid_hex   []byte
	Entrypoint string
	Method     string
	Sc_args    rpc.Arguments
	Sender     string
	Payloads   []transaction.AssetPayload
	Fees       uint64
	Height     int64
}

type BurnTXParse struct {
	Txid string
	Scid string
	Fees string
}

type NormalTXWithSCIDParse struct {
	Txid   string
	Scid   string
	Fees   uint64
	Height int64
}

type MBLInfo struct {
	Hash  string
	Miner string
}

type APIConfig struct {
	Enabled              bool   `json:"enabled"`
	Listen               string `json:"listen"`
	StatsCollectInterval string `json:"statsCollectInterval"`
	HashrateWindow       string `json:"hashrateWindow"`
	Payments             int64  `json:"payments"`
	Blocks               int64  `json:"blocks"`
	SSL                  bool   `json:"ssl"`
	SSLListen            string `json:"sslListen"`
	GetInfoSSLListen     string `json:"getInfoSSLListen"`
	CertFile             string `json:"certFile"`
	GetInfoCertFile      string `json:"getInfoCertFile"`
	KeyFile              string `json:"keyFile"`
	GetInfoKeyFile       string `json:"getInfoKeyFile"`
	MBLLookup            bool   `json:"mbblookup"`
}

type SCIDVariable struct {
	Key   interface{}
	Value interface{}
}

type FastSyncImport struct {
	Owner   string
	Height  uint64
	Headers string
}

type GnomonSCIDQuery struct {
	Owner  string
	Height uint64
	SCID   string
}

type SCIDInteractionHeight struct {
	Heights map[int64]string
}

type BlockTxns struct {
	Topoheight int64
	Tx_hashes  []crypto.Hash
}

type GetInfo rpc.GetInfo_Result

type JSONRpcReq struct {
	Id     *json.RawMessage `json:"id"`
	Method string           `json:"method"`
	Params *json.RawMessage `json:"params"`
}

type JSONRpcResp struct {
	Id      *json.RawMessage `json:"id"`
	Version string           `json:"jsonrpc"`
	Result  interface{}      `json:"result"`
	Error   interface{}      `json:"error"`
}
