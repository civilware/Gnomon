package wsserver

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/creachadair/jrpc2"
	"github.com/sirupsen/logrus"
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

// local logger
var logger *logrus.Entry

// Starts websocket listening for web miners
func ListenWS(indexer *indexer.Indexer) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	bindAddr := "127.0.0.1:9190"

	// Err check to ensure address resolves fine
	addr, err := net.ResolveTCPAddr("tcp", bindAddr)
	if err != nil {
		logger.Fatalf("[ListenWS] Error: %v", err)
	}
	_ = addr

	WSS.mux = http.NewServeMux()

	WSS.Lock()
	WSS.srv = &http.Server{Addr: bindAddr, Handler: WSS.mux}
	WSS.Unlock()

	// Setup handler for /ws directory which web miners will connect through
	WSS.mux.HandleFunc("/ws", WSS.wshandler)

	logger.Printf("[ListenWS] Starting WSServer on %v", bindAddr)

	err = WSS.srv.ListenAndServe()
	if err != nil {
		logger.Fatalf("[ListenWS] Failed to start WSServer: %v", err)
	}
}

func (wss *WSServer) wshandler(w http.ResponseWriter, r *http.Request) {

	// TODO - do we need to implement the maximum connections here as well? - need upper end testing/stability confirmation
	// TODO - ensure you add the originpatters allowed for api urls, miner urls etc. as needed. Perhaps defined within config.json instead

	var err error
	logger.Printf("%v", w.Header())
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		//OriginPatterns: []string{"127.0.0.1:9090", "127.0.0.1:8080"},
	})
	if err != nil {
		logger.Errorf("[wshandler] Err on connection being established. %v", err)
		return
	}

	defer conn.Close(websocket.StatusInternalError, "[wshandler] Disconnected")

	for {
		logger.Printf("[wshandler] Handling client...")
		err = wss.wsHandleClient(r.Context(), conn, r)
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
			logger.Errorf("[wshandler] Websocket close status: %v", websocket.CloseStatus(err))
			return
		}
		if err != nil {
			logger.Errorf("[wshandler] Disconnected %v: %v", r.RemoteAddr, err)
			return
		}
	}
}

func (wss *WSServer) wsHandleClient(ctx context.Context, c *websocket.Conn, request *http.Request) error {
	var err error

	var req *structures.JSONRpcReq
	logger.Printf("[wsHandleClient] Reader")
	// TODO: If we can't guarantee that it's a json buffer, reader hangs until client-side WS disconnects
	err = wsjson.Read(ctx, c, &req)
	if err != nil {
		if err == io.EOF {
			logger.Errorf("[wsHandleClient] io.EOF - disconnected")
		}

		return err
	}

	switch req.Method {
	case "test":
		var params *structures.GnomonSCIDQuery

		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			logger.Errorf("[wsHandleClient] Unable to parse params")
			return err
		}

		logger.Printf("Method: %v", req.Method)
		logger.Printf("GnomonSCIDQuery: %v", params)

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: "test"}
		logger.Printf("[wsHandleClient] test Writer")
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			logger.Errorf("[wsHandleClient] err writing message: err: %v", err)

			logger.Errorf("[wsHandleClient] Server disconnect request")
			return fmt.Errorf("[wsHandleClient] Server disconnect request")
		}
	default:
		logger.Errorf("[wsHandleClient] Not login or submit method")

		logger.Errorf("[wsHandleClient] Server disconnect request")
		return fmt.Errorf("[wsHandleClient] Server disconnect request")
	}

	return err
}
