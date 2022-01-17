package structures

import "github.com/deroproject/derohe/rpc"

type Parse struct {
	Txid       string
	Scid       string
	Scid_hex   []byte
	Entrypoint string
	Method     string
	Sc_args    rpc.Arguments
	Sender     string
}
