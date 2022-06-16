package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
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

// Configures a new API server to be used
func NewApiServer(cfg *structures.APIConfig, backend *store.GravitonStore) *ApiServer {
	return &ApiServer{
		Config:  cfg,
		Backend: backend,
	}
}

// Starts the api server
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

// Sets up the non-SSL API listener
func (apiServer *ApiServer) listen() {
	log.Printf("[API] Starting API on %v\n", apiServer.Config.Listen)
	router := mux.NewRouter()
	router.HandleFunc("/api/indexedscs", apiServer.StatsIndex)
	router.HandleFunc("/api/indexbyscid", apiServer.InvokeIndexBySCID)
	router.HandleFunc("/api/scvarsbyheight", apiServer.InvokeSCVarsByHeight)
	router.HandleFunc("/api/invalidscids", apiServer.InvalidSCIDStats)
	router.HandleFunc("/api/scidprivtx", apiServer.NormalTxWithSCID)
	if apiServer.Config.MBLLookup {
		router.HandleFunc("/api/getmbladdrsbyhash", apiServer.MBLLookupByHash)
		router.HandleFunc("/api/getmblcountbyaddr", apiServer.MBLLookupByAddr)
	}
	router.HandleFunc("/api/getinfo", apiServer.GetInfo)
	router.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServe(apiServer.Config.Listen, router)
	if err != nil {
		log.Fatalf("[API] Failed to start API: %v\n", err)
	}
}

// Sets up the SSL API listener
func (apiServer *ApiServer) listenSSL() {
	log.Printf("[API] Starting SSL API on %v\n", apiServer.Config.SSLListen)
	routerSSL := mux.NewRouter()
	routerSSL.HandleFunc("/api/indexedscs", apiServer.StatsIndex)
	routerSSL.HandleFunc("/api/indexbyscid", apiServer.InvokeIndexBySCID)
	routerSSL.HandleFunc("/api/scvarsbyheight", apiServer.InvokeSCVarsByHeight)
	routerSSL.HandleFunc("/api/invalidscids", apiServer.InvalidSCIDStats)
	routerSSL.HandleFunc("/api/scidprivtx", apiServer.NormalTxWithSCID)
	if apiServer.Config.MBLLookup {
		routerSSL.HandleFunc("/api/getmbladdrsbyhash", apiServer.MBLLookupByHash)
		routerSSL.HandleFunc("/api/getmblcountbyaddr", apiServer.MBLLookupByAddr)
	}
	routerSSL.HandleFunc("/api/getinfo", apiServer.GetInfo)
	routerSSL.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServeTLS(apiServer.Config.SSLListen, apiServer.Config.CertFile, apiServer.Config.KeyFile, routerSSL)
	if err != nil {
		log.Fatalf("[API] Failed to start SSL API: %v\n", err)
	}
}

// Sets up a separate getinfo SSL listener. Use cases is for things like benchmark.dero.network and others that may want to consume a https endpoint of derod getinfo or other future command output
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

// Default 404 not found response if api entry wasn't caught
func notFound(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusNotFound)
}

// Continuous check on number of validated scs etc. for base stats of service.
func (apiServer *ApiServer) collectStats() {
	stats := make(map[string]interface{})

	// Get all scid:owner
	sclist := apiServer.Backend.GetAllOwnersAndSCIDs()
	regTxCount := apiServer.Backend.GetTxCount("registration")
	burnTxCount := apiServer.Backend.GetTxCount("burn")
	normTxCount := apiServer.Backend.GetTxCount("normal")
	stats["numscs"] = len(sclist)
	stats["indexedscs"] = sclist
	stats["regTxCount"] = regTxCount
	stats["burnTxCount"] = burnTxCount
	stats["normTxCount"] = normTxCount

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
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
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
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var address string

	if !ok || len(scidkeys[0]) < 1 {
		//log.Printf("URL Param 'scid' is missing. Debugging only.\n")
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	addresskeys, ok := r.URL.Query()["address"]

	if !ok || len(addresskeys[0]) < 1 {
		//log.Printf("URL Param 'address' is missing.\n")
	} else {
		address = addresskeys[0]
	}

	// Get all scid:owner
	sclist := apiServer.Backend.GetAllOwnersAndSCIDs()

	if address != "" && scid != "" {
		// Return results that match both address and scid
		var addrscidinvokes []*structures.SCTXParse

		for k := range sclist {
			if k == scid {
				addrscidinvokes = apiServer.Backend.GetAllSCIDInvokeDetailsBySigner(scid, address)
				break
			}
		}

		reply["addrscidinvokescount"] = len(addrscidinvokes)
		reply["addrscidinvokes"] = addrscidinvokes
	} else if address != "" && scid == "" {
		// If address and no scid, return combined results of all instances address is defined (invokes and installs)
		var addrinvokes [][]*structures.SCTXParse

		for k := range sclist {
			currinvokedetails := apiServer.Backend.GetAllSCIDInvokeDetailsBySigner(k, address)

			if currinvokedetails != nil {
				addrinvokes = append(addrinvokes, currinvokedetails)
			}
		}

		reply["addrinvokescount"] = len(addrinvokes)
		reply["addrinvokes"] = addrinvokes
	} else if address == "" && scid != "" {
		// If no address and scid only, return invokes of scid
		scidinvokes := apiServer.Backend.GetAllSCIDInvokeDetails(scid)
		reply["scidinvokescount"] = len(scidinvokes)
		reply["scidinvokes"] = scidinvokes
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
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var height string

	if !ok || len(scidkeys[0]) < 1 {
		//log.Printf("URL Param 'scid' is missing. Debugging only.\n")
		reply["variables"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			log.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	heightkey, ok := r.URL.Query()["height"]

	if !ok || len(heightkey[0]) < 1 {
		//log.Printf("URL Param 'height' is missing.\n")
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

		scidInteractionHeights := apiServer.Backend.GetSCIDInteractionHeight(scid)

		interactionHeight := apiServer.getInteractionIndex(topoheight, scidInteractionHeights)

		variables = apiServer.Backend.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

		reply["variables"] = variables
		reply["scidinteractionheight"] = interactionHeight
	} else {
		// TODO: Do we need this case? Should we always require a height to be defined so as not to slow the api return due to large dataset? Do we keep but put a limit on return amount?

		variables := make(map[int64][]*structures.SCIDVariable)

		// Case to ignore all variable instance returns for builtin registration tx - large amount of data.
		if scid == "0000000000000000000000000000000000000000000000000000000000000001" {
			log.Printf("Tried to return all the sc vars of everything at registration builtin... DENIED! Too much data...")
			reply["variables"] = variables

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				log.Printf("[API] Error serializing API response: %v\n", err)
			}
			return
		}

		scidInteractionHeights := apiServer.Backend.GetSCIDInteractionHeight(scid)

		for _, h := range scidInteractionHeights {
			currVars := apiServer.Backend.GetSCIDVariableDetailsAtTopoheight(scid, h)
			variables[h] = currVars
		}

		reply["variables"] = variables
		reply["scidinteractionheights"] = scidInteractionHeights
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) NormalTxWithSCID(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var address string

	if !ok || len(scidkeys[0]) < 1 {
		//log.Printf("URL Param 'scid' is missing. Debugging only.\n")
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	addresskeys, ok := r.URL.Query()["address"]

	if !ok || len(addresskeys[0]) < 1 {
		//log.Printf("URL Param 'address' is missing.\n")
	} else {
		address = addresskeys[0]
	}

	if address == "" && scid == "" {
		reply["variables"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			log.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	}

	allNormTxWithSCIDByAddr := apiServer.Backend.GetAllNormalTxWithSCIDByAddr(address)
	allNormTxWithSCIDBySCID := apiServer.Backend.GetAllNormalTxWithSCIDBySCID(scid)

	reply["normtxwithscidbyaddr"] = allNormTxWithSCIDByAddr
	reply["normtxwithscidbyaddrcount"] = len(allNormTxWithSCIDByAddr)
	reply["normtxwithscidbyscid"] = allNormTxWithSCIDBySCID
	reply["normtxwithscidbyscidcount"] = len(allNormTxWithSCIDBySCID)

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) getInteractionIndex(topoheight int64, heights []int64) (height int64) {
	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(heights, func(i, j int) bool {
		return heights[i] > heights[j]
	})

	if topoheight > heights[0] {
		return heights[0]
	}

	for i := 1; i < len(heights); i++ {
		if heights[i] < topoheight {
			return heights[i]
		} else if heights[i] == topoheight {
			return heights[i]
		}
	}

	return height
}

func (apiServer *ApiServer) InvalidSCIDStats(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	invalidscids := apiServer.Backend.GetInvalidSCIDDeploys()
	reply["invalidscids"] = invalidscids

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) MBLLookupByHash(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	blidkeys, ok := r.URL.Query()["blid"]
	var blid string

	if !ok || len(blidkeys[0]) < 1 {
		//log.Printf("URL Param 'blid' is missing. Debugging only.\n")
		reply["mbl"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			log.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	} else {
		blid = blidkeys[0]
	}

	allMiniBlocksByBlid := apiServer.Backend.GetMiniblockDetailsByHash(blid)

	reply["mbl"] = allMiniBlocksByBlid

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) MBLLookupByAddr(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	addrkeys, ok := r.URL.Query()["address"]
	var addr string

	if !ok || len(addrkeys[0]) < 1 {
		//log.Printf("URL Param 'address' is missing. Debugging only.\n")
		reply["mbl"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			log.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	} else {
		addr = addrkeys[0]
	}

	allMiniBlocksByAddr := apiServer.Backend.GetMiniblockCountByAddress(addr)

	reply["mbl"] = allMiniBlocksByAddr

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) MBLLookupAll(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	allMiniBlocks := apiServer.Backend.GetAllMiniblockDetails()

	reply["mbl"] = allMiniBlocks

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
