package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	store "github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"
	"github.com/gorilla/mux"
)

type ApiServer struct {
	Config    *structures.APIConfig
	Stats     atomic.Value
	StatsIntv time.Duration
	Backend   *store.GravitonStore
}

func NewApiServer(cfg *structures.APIConfig, backend *store.GravitonStore) *ApiServer {
	return &ApiServer{
		Config:  cfg,
		Backend: backend,
	}
}

func (apiServer *ApiServer) Start() {

	apiServer.StatsIntv, _ = time.ParseDuration(apiServer.Config.StatsCollectInterval)
	statsTimer := time.NewTimer(apiServer.StatsIntv)
	log.Printf("[API] Set stats collect interval to %v\n", apiServer.StatsIntv)

	apiServer.collectStats()

	go func() {
		for {
			select {
			case <-statsTimer.C:
				apiServer.collectStats()
				statsTimer.Reset(apiServer.StatsIntv)
			}
		}
	}()

	// If SSL is configured, due to nature of listenandserve, put HTTP in go routine then call SSL afterwards so they can run in parallel. Otherwise, run http as normal
	if apiServer.Config.SSL {
		go apiServer.listen()
		go apiServer.listenSSL()
		apiServer.getInfoListenSSL()
	} else {
		apiServer.listen()
	}
}

func (apiServer *ApiServer) listen() {
	log.Printf("[API] Starting API on %v\n", apiServer.Config.Listen)
	router := mux.NewRouter()
	router.HandleFunc("/api/indexedscs", apiServer.StatsIndex)
	router.HandleFunc("/api/indexbyscid", apiServer.InvokeIndexBySCID)
	router.HandleFunc("/api/scvarsbyheight", apiServer.InvokeSCVarsByHeight)
	router.HandleFunc("/api/getinfo", apiServer.GetInfo)
	router.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServe(apiServer.Config.Listen, router)
	if err != nil {
		log.Fatalf("[API] Failed to start API: %v\n", err)
	}
}

func (apiServer *ApiServer) listenSSL() {
	log.Printf("[API] Starting SSL API on %v\n", apiServer.Config.SSLListen)
	routerSSL := mux.NewRouter()
	routerSSL.HandleFunc("/api/indexedscs", apiServer.StatsIndex)
	routerSSL.HandleFunc("/api/indexbyscid", apiServer.InvokeIndexBySCID)
	routerSSL.HandleFunc("/api/scvarsbyheight", apiServer.InvokeSCVarsByHeight)
	routerSSL.HandleFunc("/api/getinfo", apiServer.GetInfo)
	routerSSL.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServeTLS(apiServer.Config.SSLListen, apiServer.Config.CertFile, apiServer.Config.KeyFile, routerSSL)
	if err != nil {
		log.Fatalf("[API] Failed to start SSL API: %v\n", err)
	}
}

func (apiServer *ApiServer) getInfoListenSSL() {
	log.Printf("[API] Starting GetInfo SSL API on %v\n", apiServer.Config.GetInfoSSLListen)
	routerSSL := mux.NewRouter()
	routerSSL.HandleFunc("/api/getinfo", apiServer.GetInfo)
	routerSSL.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServeTLS(apiServer.Config.GetInfoSSLListen, apiServer.Config.GetInfoCertFile, apiServer.Config.GetInfoKeyFile, routerSSL)
	if err != nil {
		log.Fatalf("[API] Failed to start GetInfo SSL API: %v\n", err)
	}
}

func notFound(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusNotFound)
}

func (apiServer *ApiServer) collectStats() {
	stats := make(map[string]interface{})

	// Get all scid:owner
	sclist := apiServer.Backend.GetAllOwnersAndSCIDs()
	stats["numscs"] = len(sclist)
	stats["indexedscs"] = sclist

	apiServer.Stats.Store(stats)
}

func (apiServer *ApiServer) StatsIndex(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["indexedscs"] = stats["indexedscs"]
	} else {
		// Default reply - for testing etc.
		reply["hello"] = "world"
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) InvokeIndexBySCID(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
	} else {
		// Default reply - for testing etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var address string

	if !ok || len(scidkeys[0]) < 1 {
		log.Printf("URL Param 'scid' is missing. Debugging only.\n")
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	addresskeys, ok := r.URL.Query()["address"]

	if !ok || len(addresskeys[0]) < 1 {
		log.Printf("URL Param 'address' is missing.\n")
	} else {
		address = addresskeys[0]
	}

	// Get all scid:owner
	sclist := apiServer.Backend.GetAllOwnersAndSCIDs()

	if address != "" && scid != "" {
		// Return results that match both address and scid
		var addrscidinvokes []*structures.Parse

		for k, _ := range sclist {
			if k == scid {
				addrscidinvokes = apiServer.Backend.GetAllSCIDInvokeDetailsBySigner(scid, address)
				break
			}
		}

		reply["addrscidinvokes"] = addrscidinvokes
	} else if address != "" && scid == "" {
		// If address and no scid, return combined results of all instances address is defined (invokes and installs)
		var addrinvokes [][]*structures.Parse

		for k, _ := range sclist {
			currinvokedetails := apiServer.Backend.GetAllSCIDInvokeDetailsBySigner(k, address)

			if currinvokedetails != nil {
				addrinvokes = append(addrinvokes, currinvokedetails)
			}
		}

		reply["addrinvokes"] = addrinvokes
	} else if address == "" && scid != "" {
		// If no address and scid only, return invokes of scid
		reply["scidinvokes"] = apiServer.Backend.GetAllSCIDInvokeDetails(scid)
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) InvokeSCVarsByHeight(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
	} else {
		// Default reply - for testing etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var height string

	if !ok || len(scidkeys[0]) < 1 {
		log.Printf("URL Param 'scid' is missing. Debugging only.\n")
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			log.Printf("[API] Error serializing API response: %v\n", err)
		}
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	heightkey, ok := r.URL.Query()["height"]

	if !ok || len(heightkey[0]) < 1 {
		log.Printf("URL Param 'height' is missing.\n")
	} else {
		height = heightkey[0]
	}

	if height != "" {
		var variables []*structures.SCIDVariable
		var err error

		var topoheight int64
		topoheight, err = strconv.ParseInt(height, 10, 64)
		if err != nil {
			log.Printf("Err converting '%v' to int64 - %v", height, err)

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				log.Printf("[API] Error serializing API response: %v\n", err)
			}
		}

		variables = apiServer.Backend.GetSCIDVariableDetailsAtTopoheight(scid, topoheight)

		reply["variables"] = variables
	} else {
		variables := make(map[int64][]*structures.SCIDVariable)

		//variables = apiServer.Backend.GetAllSCIDVariableDetails(scid)

		reply["variables"] = variables
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) GetInfo(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	info := apiServer.Backend.GetGetInfoDetails()
	reply["getinfo"] = info

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) getStats() map[string]interface{} {
	stats := apiServer.Stats.Load()
	if stats != nil {
		return stats.(map[string]interface{})
	}
	return nil
}
