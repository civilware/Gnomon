package api

import (
	"encoding/json"
	"fmt"
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
	Config        *structures.APIConfig
	Stats         atomic.Value
	StatsIntv     time.Duration
	GravDBBackend *store.GravitonStore
	BBSBackend    *store.BboltStore
	DBType        string
}

// Configures a new API server to be used
func NewApiServer(cfg *structures.APIConfig, gravdbbackend *store.GravitonStore, bbsbackend *store.BboltStore, dbtype string) *ApiServer {
	return &ApiServer{
		Config:        cfg,
		GravDBBackend: gravdbbackend,
		BBSBackend:    bbsbackend,
		DBType:        dbtype,
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
	switch apiServer.DBType {
	case "gravdb":
		if apiServer.GravDBBackend.Closing {
			return
		}
	case "boltdb":
		if apiServer.BBSBackend.Closing {
			return
		}
	}

	stats := make(map[string]interface{})
	sclist := make(map[string]string)

	// TODO: Removeme
	var scinstalls []*structures.SCTXParse
	switch apiServer.DBType {
	case "gravdb":
		sclist = apiServer.GravDBBackend.GetAllOwnersAndSCIDs()
	case "boltdb":
		sclist = apiServer.BBSBackend.GetAllOwnersAndSCIDs()
	}
	for k, _ := range sclist {
		switch apiServer.DBType {
		case "gravdb":
			if apiServer.GravDBBackend.Closing {
				return
			}
		case "boltdb":
			if apiServer.BBSBackend.Closing {
				return
			}
		}

		var invokedetails []*structures.SCTXParse
		switch apiServer.DBType {
		case "gravdb":
			invokedetails = apiServer.GravDBBackend.GetAllSCIDInvokeDetails(k)
		case "boltdb":
			invokedetails = apiServer.BBSBackend.GetAllSCIDInvokeDetails(k)
		}
		i := 0
		for _, v := range invokedetails {
			sc_action := fmt.Sprintf("%v", v.Sc_args.Value("SC_ACTION", "U"))
			if sc_action == "1" {
				i++
				scinstalls = append(scinstalls, v)
				//log.Printf("%v - %v", v.Scid, v.Height)
			}
		}
	}

	if len(scinstalls) > 0 {
		// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
		sort.SliceStable(scinstalls, func(i, j int) bool {
			return scinstalls[i].Height < scinstalls[j].Height
		})
	}

	var lastQueries []*structures.GnomonSCIDQuery

	for _, v := range scinstalls {
		curr := &structures.GnomonSCIDQuery{Owner: v.Sender, Height: uint64(v.Height), SCID: v.Scid}
		lastQueries = append(lastQueries, curr)
	}

	// Get all scid:owner
	// TODO: Re-add
	//sclist := apiServer.Backend.GetAllOwnersAndSCIDs()
	var regTxCount, burnTxCount, normTxCount int64
	switch apiServer.DBType {
	case "gravdb":
		regTxCount = apiServer.GravDBBackend.GetTxCount("registration")
		burnTxCount = apiServer.GravDBBackend.GetTxCount("burn")
		normTxCount = apiServer.GravDBBackend.GetTxCount("normal")
	case "boltdb":
		regTxCount = apiServer.BBSBackend.GetTxCount("registration")
		burnTxCount = apiServer.BBSBackend.GetTxCount("burn")
		normTxCount = apiServer.BBSBackend.GetTxCount("normal")
	}

	stats["numscs"] = len(sclist)
	stats["indexedscs"] = sclist
	stats["indexdetails"] = lastQueries
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
		reply["indexdetails"] = stats["indexdetails"]
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
	sclist := make(map[string]string)
	switch apiServer.DBType {
	case "gravdb":
		sclist = apiServer.GravDBBackend.GetAllOwnersAndSCIDs()
	case "boltdb":
		sclist = apiServer.BBSBackend.GetAllOwnersAndSCIDs()
	}

	if address != "" && scid != "" {
		// Return results that match both address and scid
		var addrscidinvokes []*structures.SCTXParse

		for k := range sclist {
			if k == scid {
				switch apiServer.DBType {
				case "gravdb":
					addrscidinvokes = apiServer.GravDBBackend.GetAllSCIDInvokeDetailsBySigner(scid, address)
				case "boltdb":
					addrscidinvokes = apiServer.BBSBackend.GetAllSCIDInvokeDetailsBySigner(scid, address)
				}
				break
			}
		}

		reply["addrscidinvokescount"] = len(addrscidinvokes)
		reply["addrscidinvokes"] = addrscidinvokes
	} else if address != "" && scid == "" {
		// If address and no scid, return combined results of all instances address is defined (invokes and installs)
		var addrinvokes [][]*structures.SCTXParse

		for k := range sclist {
			var currinvokedetails []*structures.SCTXParse
			switch apiServer.DBType {
			case "gravdb":
				currinvokedetails = apiServer.GravDBBackend.GetAllSCIDInvokeDetailsBySigner(k, address)
			case "boltdb":
				currinvokedetails = apiServer.BBSBackend.GetAllSCIDInvokeDetailsBySigner(k, address)
			}

			if currinvokedetails != nil {
				addrinvokes = append(addrinvokes, currinvokedetails)
			}
		}

		reply["addrinvokescount"] = len(addrinvokes)
		reply["addrinvokes"] = addrinvokes
	} else if address == "" && scid != "" {
		// If no address and scid only, return invokes of scid
		var scidinvokes []*structures.SCTXParse
		switch apiServer.DBType {
		case "gravdb":
			scidinvokes = apiServer.GravDBBackend.GetAllSCIDInvokeDetails(scid)
		case "boltdb":
			scidinvokes = apiServer.BBSBackend.GetAllSCIDInvokeDetails(scid)
		}
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
		var scidInteractionHeights []int64
		var interactionHeight int64

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

		switch apiServer.DBType {
		case "gravdb":
			scidInteractionHeights = apiServer.GravDBBackend.GetSCIDInteractionHeight(scid)

			interactionHeight = apiServer.GravDBBackend.GetInteractionIndex(topoheight, scidInteractionHeights, false)

			// TODO: If there's no interaction height, do we go get scvars against daemon and store?
			variables = apiServer.GravDBBackend.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)
		case "boltdb":
			scidInteractionHeights = apiServer.BBSBackend.GetSCIDInteractionHeight(scid)

			interactionHeight = apiServer.BBSBackend.GetInteractionIndex(topoheight, scidInteractionHeights, false)

			// TODO: If there's no interaction height, do we go get scvars against daemon and store?
			variables = apiServer.BBSBackend.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)
		}

		reply["variables"] = variables
		reply["scidinteractionheight"] = interactionHeight
	} else {
		// TODO: Do we need this case? Should we always require a height to be defined so as not to slow the api return due to large dataset? Do we keep but put a limit on return amount?

		variables := make(map[int64][]*structures.SCIDVariable)
		var scidInteractionHeights []int64
		var currVars []*structures.SCIDVariable

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

		switch apiServer.DBType {
		case "gravdb":
			scidInteractionHeights = apiServer.GravDBBackend.GetSCIDInteractionHeight(scid)

			for _, h := range scidInteractionHeights {
				currVars = apiServer.GravDBBackend.GetSCIDVariableDetailsAtTopoheight(scid, h)
				variables[h] = currVars
			}
		case "boltdb":
			scidInteractionHeights = apiServer.BBSBackend.GetSCIDInteractionHeight(scid)

			for _, h := range scidInteractionHeights {
				currVars = apiServer.BBSBackend.GetSCIDVariableDetailsAtTopoheight(scid, h)
				variables[h] = currVars
			}
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

	var allNormTxWithSCIDByAddr []*structures.NormalTXWithSCIDParse
	var allNormTxWithSCIDBySCID []*structures.NormalTXWithSCIDParse

	switch apiServer.DBType {
	case "gravdb":
		allNormTxWithSCIDByAddr = apiServer.GravDBBackend.GetAllNormalTxWithSCIDByAddr(address)
		allNormTxWithSCIDBySCID = apiServer.GravDBBackend.GetAllNormalTxWithSCIDBySCID(scid)
	case "boltdb":
		allNormTxWithSCIDByAddr = apiServer.BBSBackend.GetAllNormalTxWithSCIDByAddr(address)
		allNormTxWithSCIDBySCID = apiServer.BBSBackend.GetAllNormalTxWithSCIDBySCID(scid)
	}

	reply["normtxwithscidbyaddr"] = allNormTxWithSCIDByAddr
	reply["normtxwithscidbyaddrcount"] = len(allNormTxWithSCIDByAddr)
	reply["normtxwithscidbyscid"] = allNormTxWithSCIDBySCID
	reply["normtxwithscidbyscidcount"] = len(allNormTxWithSCIDBySCID)

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		log.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) InvalidSCIDStats(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})
	invalidscids := make(map[string]uint64)

	switch apiServer.DBType {
	case "gravdb":
		invalidscids = apiServer.GravDBBackend.GetInvalidSCIDDeploys()
	case "boltdb":
		invalidscids = apiServer.BBSBackend.GetInvalidSCIDDeploys()
	}
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

	var allMiniBlocksByBlid []*structures.MBLInfo

	switch apiServer.DBType {
	case "gravdb":
		allMiniBlocksByBlid = apiServer.GravDBBackend.GetMiniblockDetailsByHash(blid)
	case "boltdb":
		allMiniBlocksByBlid = apiServer.BBSBackend.GetMiniblockDetailsByHash(blid)
	}

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

	var allMiniBlocksByAddr int64
	switch apiServer.DBType {
	case "gravdb":
		allMiniBlocksByAddr = apiServer.GravDBBackend.GetMiniblockCountByAddress(addr)
	case "boltdb":
		allMiniBlocksByAddr = apiServer.BBSBackend.GetMiniblockCountByAddress(addr)
	}

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

	allMiniBlocks := make(map[string][]*structures.MBLInfo)
	switch apiServer.DBType {
	case "gravdb":
		allMiniBlocks = apiServer.GravDBBackend.GetAllMiniblockDetails()
	case "boltdb":
		allMiniBlocks = apiServer.BBSBackend.GetAllMiniblockDetails()
	}

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

	var info *structures.GetInfo
	switch apiServer.DBType {
	case "gravdb":
		info = apiServer.GravDBBackend.GetGetInfoDetails()
	case "boltdb":
		info = apiServer.BBSBackend.GetGetInfoDetails()
	}

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
