package structures

import (
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
)

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
	Key   string
	Value string
}

type SCIDInteractionHeight struct {
	Heights map[int64]string
}

type GetInfo rpc.GetInfo_Result
