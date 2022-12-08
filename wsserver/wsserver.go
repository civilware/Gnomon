package wsserver

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/creachadair/jrpc2"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

var ioTimeout = flag.Duration("io_timeout", time.Millisecond*100, "i/o operations timeout")

type WSServer struct {
	srv *http.Server
	mux *http.ServeMux
	sync.RWMutex
	Writer io.WriteCloser
	Reader io.Reader
}

var WSS *WSServer = &WSServer{}

var options = &jrpc2.ServerOptions{AllowPush: true}

// Starts websocket listening for web miners
func ListenWS(indexer *indexer.Indexer) {
	bindAddr := "127.0.0.1:9190"

	// Err check to ensure address resolves fine
	addr, err := net.ResolveTCPAddr("tcp", bindAddr)
	if err != nil {
		log.Fatalf("[ListenWS] Error: %v", err)
	}
	_ = addr

	WSS.mux = http.NewServeMux()

	WSS.Lock()
	WSS.srv = &http.Server{Addr: bindAddr, Handler: WSS.mux}
	WSS.Unlock()

	// Setup handler for /ws directory which web miners will connect through
	WSS.mux.HandleFunc("/ws", WSS.wshandler)

	log.Printf("[ListenWS] Starting WSServer on %v", bindAddr)

	err = WSS.srv.ListenAndServe()
	if err != nil {
		log.Fatalf("[ListenWS] Failed to start WSServer: %v", err)
	}
}

func (wss *WSServer) wshandler(w http.ResponseWriter, r *http.Request) {

	// TODO - do we need to implement the maximum connections here as well? - need upper end testing/stability confirmation
	// TODO - ensure you add the originpatters allowed for api urls, miner urls etc. as needed. Perhaps defined within config.json instead

	var err error
	log.Printf("%v", w.Header())
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		//OriginPatterns: []string{"127.0.0.1:9090", "127.0.0.1:8080"},
	})
	if err != nil {
		log.Printf("[wshandler] Err on connection being established. %v", err)
		return
	}

	defer conn.Close(websocket.StatusInternalError, "[wshandler] Disconnected")

	for {
		log.Printf("[wshandler] Handling client...")
		err = wss.wsHandleClient(r.Context(), conn, r)
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
			log.Printf("[wshandler] Websocket close status: %v", websocket.CloseStatus(err))
			return
		}
		if err != nil {
			log.Printf("[wshandler] Disconnected %v: %v", r.RemoteAddr, err)
			return
		}
	}
}

func (wss *WSServer) wsHandleClient(ctx context.Context, c *websocket.Conn, request *http.Request) error {
	var err error

	var req *structures.JSONRpcReq
	log.Printf("[wsHandleClient] Reader")
	// TODO: If we can't guarantee that it's a json buffer, reader hangs until client-side WS disconnects
	err = wsjson.Read(ctx, c, &req)
	if err != nil {
		if err == io.EOF {
			log.Printf("[wsHandleClient] io.EOF - disconnected")
		}

		return err
	}

	switch req.Method {
	case "test":
		var params *structures.GnomonSCIDQuery

		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			log.Printf("[wsHandleClient] Unable to parse params")
			return err
		}

		log.Printf("Method: %v\n", req.Method)
		log.Printf("GnomonSCIDQuery: %v\n", params)

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: "test"}
		log.Printf("[wsHandleClient] test Writer")
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			log.Printf("[wsHandleClient] err writing message: err: %v", err)

			log.Printf("[wsHandleClient] Server disconnect request")
			return fmt.Errorf("[wsHandleClient] Server disconnect request")
		}
	default:
		log.Printf("[wsHandleClient] Not login or submit method")

		log.Printf("[wsHandleClient] Server disconnect request")
		return fmt.Errorf("[wsHandleClient] Server disconnect request")
	}

	return err
}
