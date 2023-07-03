package main

import (
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/civilware/Gnomon/api"
	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/mbllookup"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"

	"github.com/deroproject/derohe/cryptography/crypto"

	"github.com/docopt/docopt-go"

	"github.com/sirupsen/logrus"
)

type GnomonServer struct {
	LastIndexedHeight int64
	SearchFilters     []string
	Indexers          map[string]*indexer.Indexer
	Closing           bool
	DaemonEndpoint    string
	RunMode           string
	DBType            string
	MBLLookup         bool
}

var command_line string = `Gnomon
Gnomon Indexing Service: Index DERO's blockchain for Smart Contract deployments/listings/etc. as well as other data analysis.

Usage:
  gnomonindexer [options]
  gnomonindexer -h | --help

Options:
  -h --help     Show this screen.
  --daemon-rpc-address=<127.0.0.1:40402>    Connect to daemon.
  --api-address=<127.0.0.1:8082>     Host api.
  --enable-api-ssl=<false>     Enable ssl.
  --api-ssl-address=<127.0.0.1:9092>     Host ssl api.
  --get-info-ssl-address=<127.0.0.1:9394>     Host GetInfo ssl api. This is to completely isolate it from gnomon api results as a whole. Normal api endpoints also surface the getinfo call if needed.
  --start-topoheight=<31170>     Define a start topoheight other than 1 if required to index at a higher block (pruned db etc.).
  --search-filter=<"Function InputStr(input String, varname String) Uint64">     Defines a search filter to match on installed SCs to add to validated list and index all actions, this will most likely change in the future but can allow for some small variability. Include escapes etc. if required. If nothing is defined, it will pull all (minus hardcoded sc).
  --runmode=<daemon>     Defines the runmode of gnomon (daemon/wallet/asset). By default this is daemon mode which indexes directly from the chain. Wallet mode indexes from wallet tx history (use/store with caution).
  --enable-miniblock-lookup=<false>     True/false value to store all miniblocks and their respective details and miner addresses who found them. This currently REQUIRES a full node db in same directory
  --close-on-disconnect=<false>     True/false value to close out indexers in the event of daemon disconnect. Daemon will fail connections for 30 seconds and then close the indexer. This is for HA pairs or wanting services off on disconnect.
  --fastsync=<false>     True/false value to define loading at chain height and only keeping track of list of SCIDs and their respective up-to-date variable stores as it hits them. NOTE: You will not get all information and may rely on manual scid additions.
  --dbtype=<boltdb>     Defines type of database. 'gravdb' or 'boltdb'. If gravdb, expect LARGE local storage if running in daemon mode until further optimized later. [--ramstore can only be valid with gravdb]. Defaults to boltdb.
  --ramstore=<false>     True/false value to define if the db [only if gravdb] will be used in RAM or on disk. Keep in mind on close, the RAM store will be non-persistent.
  --num-parallel-blocks=<5>     Defines the number of parallel blocks to index in daemonmode. While a lower limit of 1 is defined, there is no hardcoded upper limit. Be mindful the higher set, the greater the daemon load potentially (highly recommend local nodes if this is greater than 1-5)
  --debug     Enables debug logging`

var Exit_In_Progress = make(chan bool)

var closeondisconnect bool
var fastsync bool
var ramstore bool
var version = "0.1.2"

var RLI *readline.Instance

var Gnomon = &GnomonServer{}

// local logger
var logger *logrus.Entry

// TODO: Add as a passable param perhaps? Or other. Using ;;; for now, can be anything really.. just think what isn't used in norm SC code iterations
const sf_separator = ";;;"

func main() {
	var err error

	n := runtime.NumCPU()
	runtime.GOMAXPROCS(n)

	Gnomon.Indexers = make(map[string]*indexer.Indexer)

	// Inspect argument(s)
	arguments, err := docopt.ParseArgs(command_line, nil, version)
	if err != nil {
		log.Fatalf("[Main] Error while parsing arguments err: %s\n", err)
	}

	// Readline GNOMON
	RLI, err = readline.NewEx(&readline.Config{
		Prompt:          "\033[92mGNOMON\033[32m>>>\033[0m ",
		HistoryFile:     filepath.Join(os.TempDir(), "derod_readline.tmp"),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",

		HistorySearchFold:   true,
		FuncFilterInputRune: filterInput,
	})
	if err != nil {
		fmt.Printf("Error starting readline err: %s\n", err)
		return
	}
	defer RLI.Close()

	// setup logging
	indexer.InitLog(arguments, RLI.Stdout())
	logger = structures.Logger.WithFields(logrus.Fields{})

	// Set variables from arguments
	daemon_endpoint := "127.0.0.1:40402"
	if arguments["--daemon-rpc-address"] != nil {
		daemon_endpoint = arguments["--daemon-rpc-address"].(string)
	}
	Gnomon.DaemonEndpoint = daemon_endpoint

	logger.Printf("[Main] Using daemon RPC endpoint %s\n", daemon_endpoint)

	api_endpoint := "127.0.0.1:8082"
	if arguments["--api-address"] != nil {
		api_endpoint = arguments["--api-address"].(string)
	}

	api_ssl_endpoint := "127.0.0.1:9092"
	if arguments["--api-ssl-address"] != nil {
		api_ssl_endpoint = arguments["--api-ssl-address"].(string)
	}

	get_info_ssl_endpoint := "127.0.0.1:9394"
	if arguments["--get-info-ssl-address"] != nil {
		get_info_ssl_endpoint = arguments["--get-info-ssl-address"].(string)
	}

	sslenabled := false
	if arguments["--enable-api-ssl"] != nil {
		sslenablestr := arguments["--enable-api-ssl"].(string)
		if sslenablestr == "true" {
			sslenabled = true
		}
	}

	Gnomon.RunMode = "daemon"
	if arguments["--runmode"] != nil {
		if arguments["--runmode"] == "daemon" || arguments["--runmode"] == "wallet" || arguments["--runmode"] == "asset" {
			Gnomon.RunMode = arguments["--runmode"].(string)
		} else {
			logger.Fatalf("[Main] ERR - Runmode must be either 'daemon' or 'wallet'")
			return
		}
	}

	last_indexedheight := int64(1)
	if arguments["--start-topoheight"] != nil {
		last_indexedheight, err = strconv.ParseInt(arguments["--start-topoheight"].(string), 10, 64)
		if err != nil {
			logger.Fatalf("[Main] ERROR while converting --start-topoheight to int64\n")
			return
		}
	}

	var search_filter []string
	if arguments["--search-filter"] != nil {
		search_filter_nonarr := arguments["--search-filter"].(string)
		search_filter = strings.Split(search_filter_nonarr, sf_separator)
		logger.Printf("[Main] Using search filter: %v\n", search_filter)
	} else {
		logger.Printf("[Main] No search filter defined.. grabbing all.\n")
	}

	mbl := false
	if arguments["--enable-miniblock-lookup"] != nil {
		mbllookupstr := arguments["--enable-miniblock-lookup"].(string)
		if mbllookupstr == "true" {
			mbl = true
		}

		err = mbllookup.DeroDB.LoadDeroDB()
		if err != nil {
			logger.Fatalf("[Main] ERR Loading DeroDB - Be sure to run from directory of fully synced mainnet - %v\n", err)
			return
		}
	}
	Gnomon.MBLLookup = mbl

	numParallelBlocks := 1
	if arguments["--num-parallel-blocks"] != nil {
		numParallelBlocks, err = strconv.Atoi(arguments["--num-parallel-blocks"].(string))
		if err != nil {
			logger.Fatalf("[Main] ERR converting '%v' to int for --num-parallel-blocks.", arguments["--num-parllel-blocks"].(string))
		}
	}

	// Edge flag to be able to close on disconnect from a daemon after x failures. Can be used for smaller nodes or other areas where you want the API to offline when no new data is ingested/indexed.
	if arguments["--close-on-disconnect"] != nil {
		closeondisconnectstr := arguments["--close-on-disconnect"].(string)
		if closeondisconnectstr == "true" {
			closeondisconnect = true
		}
	}

	// Starts at current chainheight and retrieves a list of SCIDs to auto-add to index validation list
	if arguments["--fastsync"] != nil {
		fastsyncstr := arguments["--fastsync"].(string)
		if fastsyncstr == "true" {
			fastsync = true
		}
	}

	Gnomon.DBType = "boltdb"
	if arguments["--dbtype"] != nil {
		if arguments["--dbtype"] == "boltdb" || arguments["--dbtype"] == "gravdb" {
			Gnomon.DBType = arguments["--dbtype"].(string)
		} else {
			logger.Fatalf("[Main] ERR - dbtype must be either 'boltdb' or 'gravdb'")
			return
		}
	}

	// Uses RAM store for grav db
	if arguments["--ramstore"] != nil && Gnomon.DBType == "gravdb" {
		ramstorestr := arguments["--ramstore"].(string)
		if ramstorestr == "true" {
			ramstore = true
		}
	}

	// Database
	var Graviton_backend *storage.GravitonStore
	var Bbs_backend *storage.BboltStore
	var csearch_filter string

	switch Gnomon.DBType {
	case "gravdb":
		if ramstore {
			Graviton_backend, err = storage.NewGravDBRAM("25ms")
			if err != nil {
				logger.Fatalf("[Main] Err creating gravdb: %v", err)
			}
		} else {
			var shasum string
			if len(search_filter) == 0 {
				shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
			} else {
				csearch_filter = strings.Join(search_filter, sf_separator)
				shasum = fmt.Sprintf("%x", sha1.Sum([]byte(csearch_filter)))
			}
			db_folder := fmt.Sprintf("gnomondb\\%s_%s", "GNOMON", shasum)
			current_path, err := os.Getwd()
			if err != nil {
				logger.Fatalf("[Main] Err getting working directory: %v", err)
			}
			db_path := filepath.Join(current_path, db_folder)
			Graviton_backend, err = storage.NewGravDB(db_path, "25ms")
			if err != nil {
				logger.Fatalf("[Main] Err creating gravdb: %v", err)
			}
		}

	case "boltdb":
		var shasum string
		if len(search_filter) == 0 {
			shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
		} else {
			csearch_filter = strings.Join(search_filter, sf_separator)
			shasum = fmt.Sprintf("%x", sha1.Sum([]byte(csearch_filter)))
		}
		db_name := fmt.Sprintf("%s_%s.db", "GNOMON", shasum)
		current_path, err := os.Getwd()
		if err != nil {
			logger.Fatalf("[Main] Err getting working directory: %v", err)
		}
		db_path := filepath.Join(current_path, db_name)
		Bbs_backend, err = storage.NewBBoltDB(db_path, db_name)
	}

	// API
	apic := &structures.APIConfig{
		Enabled:              true,
		Listen:               api_endpoint,
		StatsCollectInterval: "5s",
		SSL:                  sslenabled,
		SSLListen:            api_ssl_endpoint,
		GetInfoSSLListen:     get_info_ssl_endpoint,
		CertFile:             "fullchain.cer",
		GetInfoCertFile:      "getinfofullchain.cer",
		KeyFile:              "cert.key",
		GetInfoKeyFile:       "getinfocert.key",
		MBLLookup:            mbl,
	}
	// TODO: Add default search filter index of sorts, rather than passing through Graviton_backend object as a whole
	apis := api.NewApiServer(apic, Graviton_backend, Bbs_backend, Gnomon.DBType)
	go apis.Start()

	// Start default indexer based on search_filter params
	defaultIndexer := indexer.NewIndexer(Graviton_backend, Bbs_backend, Gnomon.DBType, search_filter, last_indexedheight, daemon_endpoint, Gnomon.RunMode, mbl, closeondisconnect, fastsync)

	switch Gnomon.RunMode {
	case "daemon":
		go defaultIndexer.StartDaemonMode(numParallelBlocks)
	case "wallet":
		go defaultIndexer.StartWalletMode("")
	case "asset":
		go defaultIndexer.StartDaemonMode(numParallelBlocks)
	default:
		go defaultIndexer.StartDaemonMode(numParallelBlocks)
	}
	Gnomon.Indexers[csearch_filter] = defaultIndexer

	go func() {
		for {
			if err = Gnomon.readline_loop(RLI); err == nil {
				break
			}
		}
	}()

	// This tiny goroutine continuously updates status as required
	go func() {
		for {
			select {
			case <-Exit_In_Progress:
				Gnomon.Close()
				return
			default:
			}
			if Gnomon.Closing {
				return
			}

			validatedSCIDs := make(map[string]string)
			switch Gnomon.DBType {
			case "gravdb":
				validatedSCIDs = Graviton_backend.GetAllOwnersAndSCIDs()
			case "boltdb":
				validatedSCIDs = Bbs_backend.GetAllOwnersAndSCIDs()
			}

			gnomon_count := int64(len(validatedSCIDs))

			currheight := defaultIndexer.LastIndexedHeight

			// choose color based on urgency
			color := "\033[32m" // default is green color
			if currheight < defaultIndexer.ChainHeight {
				color = "\033[33m" // make prompt yellow
			} else if currheight > defaultIndexer.ChainHeight {
				color = "\033[31m" // make prompt red
			}

			gcolor := "\033[32m" // default is green color
			if gnomon_count < 1 {
				gcolor = "\033[33m" // make prompt yellow
			}

			RLI.SetPrompt(fmt.Sprintf("\033[1m\033[32mGNOMON \033[0m"+color+"[%d/%d] "+gcolor+"R:%d G:%d >>\033[0m ", currheight, defaultIndexer.ChainHeight, gnomon_count, len(Gnomon.Indexers)))
			RLI.Refresh()
			time.Sleep(3 * time.Second)
		}
	}()

	setPasswordCfg := RLI.GenPasswordConfig()
	setPasswordCfg.SetListener(func(line []rune, pos int, key rune) (newLine []rune, newPos int, ok bool) {
		RLI.SetPrompt(fmt.Sprintf("Enter password(%v): ", len(line)))
		RLI.Refresh()
		return nil, 0, false
	})
	RLI.Refresh() // refresh the prompt

	// Hold
	select {}
}

func filterInput(r rune) (rune, bool) {
	switch r {
	// block CtrlZ feature
	case readline.CharCtrlZ:
		return r, false
	}
	return r, true
}

func (g *GnomonServer) readline_loop(l *readline.Instance) (err error) {

	defer func() {
		if r := recover(); r != nil {
			logger.Printf("[Main] Readline_loop err: %v\n", err)
			err = fmt.Errorf("crashed")
		}

	}()

	//restart_loop:
	for {
		line, err := RLI.Readline()
		if err == io.EOF {
			<-Exit_In_Progress
			return nil
		}

		if err == readline.ErrInterrupt {
			if len(line) == 0 {
				logger.Printf("[Main] Ctrl-C received, putting gnomes to sleep. This will take ~5sec.\n")
				g.Close()
				return nil
			} else {
				continue
			}
		}

		line = strings.TrimSpace(line)
		line_parts := strings.Fields(line)

		command := ""
		if len(line_parts) >= 1 {
			command = strings.ToLower(line_parts[0])
		}

		// TODO: CLI commands may not necessarily always need to print from every indexer, could produce multiple results. Issues? Maybe modify in future.
		switch {
		case line == "help":
			usage(l.Stderr())
		case line == "version":
			logger.Printf("Version: %v", version)
		case command == "listsc":
			for ki, vi := range g.Indexers {
				logger.Printf("- Indexer '%v'", ki)
				sclist := make(map[string]string)
				switch vi.DBType {
				case "gravdb":
					sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
				case "boltdb":
					sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
				}
				for k, v := range sclist {
					logger.Printf("SCID: %v ; Owner: %v\n", k, v)
				}
			}
		case command == "listsc_code":
			switch len(line_parts) {
			case 2:
				i := 0
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)

					_, sccode, _, err := vi.RPC.GetSCVariables(line_parts[1], vi.ChainHeight, nil, nil, nil)
					if err != nil {
						logger.Errorf("%v", err)
					}

					if sccode != "" {
						logger.Printf("%s", sccode)
						i++
						break
					} else {
						continue
					}
				}

				if i == 0 {
					logger.Printf("SCID '%s' code was unable to be retrieved. Is it installed?", line_parts[1])
				}
			case 3:
				if s, err := strconv.Atoi(line_parts[2]); err == nil {
					i := 0
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)

						_, sccode, _, err := vi.RPC.GetSCVariables(line_parts[1], int64(s), nil, nil, nil)
						if err != nil {
							logger.Errorf("%v", err)
						}

						if sccode != "" {
							logger.Printf("%s", sccode)
							i++
							break
						} else {
							continue
						}
					}

					if i == 0 {
						logger.Printf("SCID '%s' code was unable to be retrieved at height '%v'. Was it installed?", line_parts[1], int64(s))
					}
				} else {
					logger.Errorf("Could not parse '%v' into an int for height", line_parts[2])
				}

			default:
				logger.Printf("listsc_code needs one value: single scid\n")
			}
		case command == "listsc_variables":
			switch len(line_parts) {
			case 2:
				i := 0
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)

					vars, _, _, err := vi.RPC.GetSCVariables(line_parts[1], vi.ChainHeight, nil, nil, nil)
					if err != nil {
						logger.Errorf("%v", err)
					}

					if len(vars) > 0 {
						for _, vvar := range vars {
							if vvar.Key.(string) == "C" {
								continue
							}
							logger.Printf("Key: %v ; Value: %v", vvar.Key, vvar.Value)
						}
						i++
						break
					} else {
						continue
					}
				}

				if i == 0 {
					logger.Printf("SCID '%s' code was unable to be retrieved. Is it installed?", line_parts[1])
				}
			case 3:
				if s, err := strconv.Atoi(line_parts[2]); err == nil {
					i := 0
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)

						vars, _, _, err := vi.RPC.GetSCVariables(line_parts[1], int64(s), nil, nil, nil)
						if err != nil {
							logger.Errorf("%v", err)
						}

						if len(vars) > 0 {
							for _, vvar := range vars {
								if vvar.Key.(string) == "C" {
									continue
								}
								logger.Printf("Key: %v ; Value: %v", vvar.Key, vvar.Value)
							}
							i++
							break
						} else {
							continue
						}
					}

					if i == 0 {
						logger.Printf("SCID '%s' variables were unable to be retrieved at height '%v'. Was it installed?", line_parts[1], int64(s))
					}
				} else {
					logger.Errorf("Could not parse '%v' into an int for height", line_parts[2])
				}

			default:
				logger.Printf("listsc_variables needs one value: single scid\n")
			}
		/*
			case command == "new_sf":
					if len(line_parts) >= 2 {
						nsf := strings.Join(line_parts[1:], " ")
						nsf_j := strings.Split(nsf, sf_separator)
						logger.Printf("Adding new searchfilter '%v'\n", nsf)

						// Database
						var nBackend *storage.GravitonStore
						if fastsync || ramstore {
							nBackend, err = storage.NewGravDBRAM("25ms")
							if err != nil {
								logger.Fatalf("[new_sf] Err creating gravdb: %v", err)
							}
						} else {
							nShasum := fmt.Sprintf("%x", sha1.Sum([]byte(nsf)))
							nDBFolder := fmt.Sprintf("gnomondb\\%s_%s", "GNOMON", nShasum)
							current_path, err := os.Getwd()
							if err != nil {
								logger.Printf("%v\n", err)
							}
							db_path := filepath.Join(current_path, nDBFolder)
							logger.Printf("Adding new database '%v'\n", db_path)
							nBackend, err = storage.NewGravDB(db_path, "25ms")
							if err != nil {
								logger.Fatalf("[new_sf] Err creating gravdb: %v", err)
							}
						}

						// Start default indexer based on search_filter params
						logger.Printf("Adding new indexer. ID: '%v'; - SearchFilter: '%v'\n", len(g.Indexers)+1, nsf)
						nIndexer := indexer.NewIndexer(nBackend, nil, Gnomon.DBType, nsf_j, 0, g.DaemonEndpoint, g.RunMode, g.MBLLookup, closeondisconnect, fastsync)
						go nIndexer.StartDaemonMode(1)
						g.Indexers[nsf] = nIndexer
					}
		*/
		case command == "listsc_byowner":
			if len(line_parts) == 2 && len(line_parts[1]) == 66 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					sclist := make(map[string]string)
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, v := range sclist {
						if v == line_parts[1] {
							logger.Printf("SCID: %v ; Owner: %v\n", k, v)
							var invokedetails []*structures.SCTXParse
							switch vi.DBType {
							case "gravdb":
								invokedetails = vi.GravDBBackend.GetAllSCIDInvokeDetails(k)
							case "boltdb":
								invokedetails = vi.BBSBackend.GetAllSCIDInvokeDetails(k)
							}
							for _, invoke := range invokedetails {
								logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v\n", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
							}
							count++
						}
					}

					if count == 0 {
						logger.Printf("No SCIDs installed by %v\n", line_parts[1])
					}
				}
			} else {
				logger.Printf("listsc_byowner needs a single owner address as argument\n")
			}
		case command == "listsc_byscid":
			if len(line_parts) >= 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, v := range sclist {
						if k == line_parts[1] {
							logger.Printf("SCID: %v ; Owner: %v\n", k, v)
							var invokedetails []*structures.SCTXParse
							switch vi.DBType {
							case "gravdb":
								invokedetails = vi.GravDBBackend.GetAllSCIDInvokeDetails(k)
							case "boltdb":
								invokedetails = vi.BBSBackend.GetAllSCIDInvokeDetails(k)
							}
							for _, invoke := range invokedetails {
								if len(line_parts) == 3 {
									ca, _ := strconv.Atoi(line_parts[2])
									if invoke.Height >= int64(ca) {
										logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v\n", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
									}
								} else {
									logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v\n", invoke.Sender, invoke.Height, invoke.Sc_args, invoke.Payloads[0].BurnValue)
								}
							}
							count++
						}
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v\n", line_parts[1])
					}
				}
			} else {
				logger.Printf("listsc_byscid needs a single scid as argument\n")
			}
		case command == "listsc_byheight":
			{
				if len(line_parts) == 1 {
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)
						var scinstalls []*structures.SCTXParse
						var sclist map[string]string
						switch vi.DBType {
						case "gravdb":
							sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
						case "boltdb":
							sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
						}
						for k, _ := range sclist {
							var invokedetails []*structures.SCTXParse
							switch vi.DBType {
							case "gravdb":
								invokedetails = vi.GravDBBackend.GetAllSCIDInvokeDetails(k)
							case "boltdb":
								invokedetails = vi.BBSBackend.GetAllSCIDInvokeDetails(k)
							}
							i := 0
							for _, v := range invokedetails {
								sc_action := fmt.Sprintf("%v", v.Sc_args.Value("SC_ACTION", "U"))
								if sc_action == "1" {
									i++
									scinstalls = append(scinstalls, v)
								}
							}

							if i == 0 {
								logger.Printf("No sc_action of '1' for %v", k)
							}
						}

						if len(scinstalls) > 0 {
							// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
							sort.SliceStable(scinstalls, func(i, j int) bool {
								return scinstalls[i].Height < scinstalls[j].Height
							})

							// +1 for hardcoded name service SC
							for _, v := range scinstalls {
								logger.Printf("SCID: %v ; Owner: %v ; DeployHeight: %v", v.Scid, v.Sender, v.Height)
							}
							logger.Printf("Total SCs installed: %v", len(scinstalls)+1)
						}
					}
				} else {
					logger.Printf("listscinvoke_bysigner needs a single scid and partialsigner string as argument\n")
				}
			}
		case command == "listsc_balances":
			if len(line_parts) == 1 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, _ := range sclist {
						_, _, cbal, _ := vi.RPC.GetSCVariables(k, vi.ChainHeight, nil, nil, nil)
						var pc int
						for kb, vb := range cbal {
							if vb > 0 {
								if pc == 0 {
									fmt.Printf("%v:\n", k)
								}
								if kb == "0000000000000000000000000000000000000000000000000000000000000000" {
									fmt.Printf("_DERO: %v\n", vb)
								} else {
									fmt.Printf("_Asset: %v:%v\n", kb, vb)
								}
								pc++
							}
						}
						count++
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v\n", line_parts[1])
					}
				}
			} else {
				logger.Printf("listsc_byscid needs a single scid as argument\n")
			}
		case command == "listsc_byentrypoint":
			if len(line_parts) == 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var indexbyentry []*structures.SCTXParse
					switch vi.DBType {
					case "gravdb":
						indexbyentry = vi.GravDBBackend.GetAllSCIDInvokeDetailsByEntrypoint(line_parts[1], line_parts[2])
					case "boltdb":
						indexbyentry = vi.BBSBackend.GetAllSCIDInvokeDetailsByEntrypoint(line_parts[1], line_parts[2])
					}
					var count int64
					for _, v := range indexbyentry {
						logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v\n", v.Sender, v.Height, v.Sc_args, v.Payloads[0].BurnValue)
						count++
					}

					if count == 0 {
						logger.Printf("No SCID invokes of entrypoint '%v' for %v\n", line_parts[2], line_parts[1])
					}
				}
			} else {
				logger.Printf("listsc_byscid needs a single scid and entrypoint as argument\n")
			}
		case command == "listsc_byinitialize":
			if len(line_parts) == 1 { //&& len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count, count2 int64
					for k, _ := range sclist {
						var indexbyentry []*structures.SCTXParse
						switch vi.DBType {
						case "gravdb":
							indexbyentry = vi.GravDBBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "Initialize")
						case "boltdb":
							indexbyentry = vi.BBSBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "Initialize")
						}
						for _, v := range indexbyentry {
							logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v\n", v.Sender, v.Height, v.Sc_args, v.Payloads[0].BurnValue)
							count++
						}
						var indexbyentry2 []*structures.SCTXParse
						switch vi.DBType {
						case "gravdb":
							indexbyentry2 = vi.GravDBBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "InitializePrivate")
						case "boltdb":
							indexbyentry2 = vi.BBSBackend.GetAllSCIDInvokeDetailsByEntrypoint(k, "InitializePrivate")
						}
						for _, v := range indexbyentry2 {
							logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v\n", v.Sender, v.Height, v.Sc_args, v.Payloads[0].BurnValue)
							count2++
						}
					}

					if count == 0 && count2 == 0 {
						logger.Printf("No SCIDs with initialize called.")
					}
				}
			} else {
				logger.Printf("listsc_byscid needs a single scid and entrypoint as argument\n")
			}
		case command == "listscinvoke_bysigner":
			{
				if len(line_parts) >= 2 {
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)
						var sclist map[string]string
						switch vi.DBType {
						case "gravdb":
							sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
						case "boltdb":
							sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
						}
						for k, v := range sclist {
							if len(line_parts) > 2 && len(line_parts[2]) == 64 {
								if k != line_parts[2] {
									continue
								}
							}
							var indexbypartialsigner []*structures.SCTXParse
							switch vi.DBType {
							case "gravdb":
								indexbypartialsigner = vi.GravDBBackend.GetAllSCIDInvokeDetailsBySigner(k, line_parts[1])
							case "boltdb":
								indexbypartialsigner = vi.BBSBackend.GetAllSCIDInvokeDetailsBySigner(k, line_parts[1])
							}
							if len(indexbypartialsigner) > 0 {
								logger.Printf("SCID: %v ; Owner: %v\n", k, v)
							}
							for _, v := range indexbypartialsigner {
								logger.Printf("Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v\n", v.Sender, v.Height, v.Sc_args, v.Payloads[0].BurnValue)
							}
						}
					}
				} else {
					logger.Printf("listscinvoke_bysigner needs a single scid and partialsigner string as argument\n")
				}
			}
		case command == "listscidkey_byvaluestored":
			if len(line_parts) >= 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, _ := range sclist {
						if k == line_parts[1] {
							var keysstringbyvalue []string
							var keysuint64byvalue []uint64

							switch vi.DBType {
							case "gravdb":
								intCheck, err := strconv.Atoi(line_parts[2])
								if err != nil {
									keysstringbyvalue, keysuint64byvalue = vi.GravDBBackend.GetSCIDKeysByValue(k, strings.Join(line_parts[2:], " "), vi.ChainHeight, true)
								} else {
									keysstringbyvalue, keysuint64byvalue = vi.GravDBBackend.GetSCIDKeysByValue(k, uint64(intCheck), vi.ChainHeight, true)
								}
							case "boltdb":
								intCheck, err := strconv.Atoi(line_parts[2])
								if err != nil {
									keysstringbyvalue, keysuint64byvalue = vi.BBSBackend.GetSCIDKeysByValue(k, strings.Join(line_parts[2:], " "), vi.ChainHeight, true)
								} else {
									keysstringbyvalue, keysuint64byvalue = vi.BBSBackend.GetSCIDKeysByValue(k, uint64(intCheck), vi.ChainHeight, true)
								}
							}

							for _, skey := range keysstringbyvalue {
								logger.Printf("%v\n", skey)
							}
							for _, ukey := range keysuint64byvalue {
								logger.Printf("%v\n", ukey)
							}
							count++
						}
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v\n", line_parts[1])
					}
				}
			} else {
				logger.Printf("listscidkey_byvalue needs two values: single scid and value to match as arguments\n")
			}
		case command == "listscidkey_byvaluelive":
			if len(line_parts) >= 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var variables []*structures.SCIDVariable
					var keysstringbyvalue []string
					var keysuint64byvalue []uint64

					intCheck, err := strconv.Atoi(line_parts[2])
					if err != nil {
						keysstringbyvalue, keysuint64byvalue, _ = vi.GetSCIDKeysByValue(variables, line_parts[1], strings.Join(line_parts[2:], " "), vi.ChainHeight)
					} else {
						keysstringbyvalue, keysuint64byvalue, _ = vi.GetSCIDKeysByValue(variables, line_parts[1], uint64(intCheck), vi.ChainHeight)
					}

					for _, skey := range keysstringbyvalue {
						logger.Printf("%v\n", skey)
					}
					for _, ukey := range keysuint64byvalue {
						logger.Printf("%v\n", ukey)
					}
					// TODO: We can break, it's using the daemon to return the results. TODO Could pass mainnet/testnet and check indexers for different endpoints on different chains etc. but may not be needed
					break
				}
			} else {
				logger.Printf("listscidkey_byvalue needs two values: single scid and value to match as arguments\n")
			}
		case command == "listscidvalue_bykeystored":
			if len(line_parts) >= 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var sclist map[string]string
					switch vi.DBType {
					case "gravdb":
						sclist = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					case "boltdb":
						sclist = vi.BBSBackend.GetAllOwnersAndSCIDs()
					}
					var count int64
					for k, _ := range sclist {
						if k == line_parts[1] {
							var valuesstringbykey []string
							var valuesuint64bykey []uint64

							switch vi.DBType {
							case "gravdb":
								intCheck, err := strconv.Atoi(line_parts[2])
								if err != nil {
									valuesstringbykey, valuesuint64bykey = vi.GravDBBackend.GetSCIDValuesByKey(k, strings.Join(line_parts[2:], " "), vi.ChainHeight, true)
								} else {
									valuesstringbykey, valuesuint64bykey = vi.GravDBBackend.GetSCIDValuesByKey(k, uint64(intCheck), vi.ChainHeight, true)
								}
							case "boltdb":
								intCheck, err := strconv.Atoi(line_parts[2])
								if err != nil {
									valuesstringbykey, valuesuint64bykey = vi.GravDBBackend.GetSCIDValuesByKey(k, strings.Join(line_parts[2:], " "), vi.ChainHeight, true)
								} else {
									valuesstringbykey, valuesuint64bykey = vi.GravDBBackend.GetSCIDValuesByKey(k, uint64(intCheck), vi.ChainHeight, true)
								}
							}

							for _, sval := range valuesstringbykey {
								logger.Printf("%v\n", sval)

								var h crypto.Hash
								copy(h[:], []byte(sval)[:])
								logger.Printf("%v", h.String())
								logger.Printf("%v", []byte(sval))
							}
							for _, uval := range valuesuint64bykey {
								logger.Printf("%v\n", uval)
							}
							count++
						}
					}

					if count == 0 {
						logger.Printf("No SCIDs installed matching %v\n", line_parts[1])
					}
				}
			} else {
				logger.Printf("listscidkey_byvalue needs two values: single scid and value to match as arguments\n")
			}
		case command == "listscidvalue_bykeylive":
			if len(line_parts) >= 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var variables []*structures.SCIDVariable
					var valuesstringbykey []string
					var valuesuint64bykey []uint64

					intCheck, err := strconv.Atoi(line_parts[2])
					if err != nil {
						valuesstringbykey, valuesuint64bykey, _ = vi.GetSCIDValuesByKey(variables, line_parts[1], strings.Join(line_parts[2:], " "), vi.ChainHeight)
					} else {
						valuesstringbykey, valuesuint64bykey, _ = vi.GetSCIDValuesByKey(variables, line_parts[1], uint64(intCheck), vi.ChainHeight)
					}
					for _, sval := range valuesstringbykey {
						logger.Printf("%v\n", sval)

						// TOOD: Returning human readable string representation of a txid crypto.Hash returned from above. Perhaps a way to implement this to be discoverable based on length?
						var h crypto.Hash
						copy(h[:], []byte(sval)[:])
						logger.Printf("%v", h.String())
						logger.Printf("%v", []byte(sval))
					}
					for _, uval := range valuesuint64bykey {
						logger.Printf("%v\n", uval)
					}

					// TODO: We can break, it's using the daemon to return the results. TODO Could pass mainnet/testnet and check indexers for different endpoints on different chains etc. but may not be needed
					break
				}
			} else {
				logger.Printf("listscidkey_byvalue needs two values: single scid and value to match as arguments\n")
			}
		case command == "validatesc":
			if len(line_parts) == 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					variables, code, _, _ := vi.RPC.GetSCVariables(line_parts[1], vi.ChainHeight, nil, nil, nil)
					keysstring, _, _ := vi.GetSCIDValuesByKey(variables, line_parts[1], "signature", vi.ChainHeight)

					// Check  if keysstring is nil or not to avoid any sort of panics
					var sigstr string
					if len(keysstring) > 0 {
						sigstr = keysstring[0]
					}
					validated, signer, err := vi.ValidateSCSignature(code, sigstr)

					if err != nil {
						logger.Printf("[validatesc] ERR - %v", err)
					} else {
						logger.Printf("Validated: %v", validated)
						logger.Printf("Signer: %v", signer)
					}
					// TODO: We can break, it's using the daemon to return the results. TODO Could pass mainnet/testnet and check indexers for different endpoints on different chains etc. but may not be needed
					break
				}
			}
		case command == "addscid_toindex":
			// TODO: Perhaps add indexer id to a param so you can add it to specific search_filter/indexer. Supported by a 'status' (tbd) command which returns details of each indexer
			if len(line_parts) == 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					scidstoadd := make(map[string]*structures.FastSyncImport)
					scidstoadd[line_parts[1]] = &structures.FastSyncImport{}
					err = vi.AddSCIDToIndex(scidstoadd)
					if err != nil {
						logger.Printf("Err - %v", err)
					}
				}
			} else {
				logger.Printf("addscid_toindex needs 1 values: single scid to match as arguments\n")
			}
			/*
				case command == "index_txn":
					// TODO: Perhaps add indexer id to a param so you can add it to specific search_filter/indexer. Supported by a 'status' (tbd) command which returns details of each indexer
					if len(line_parts) == 2 && len(line_parts[1]) == 64 {
						for ki, vi := range g.Indexers {
							logger.Printf("- Indexer '%v'", ki)
							scidstoadd := make(map[string]*structures.FastSyncImport)
							scidstoadd[line_parts[1]] = &structures.FastSyncImport{}
							//err = vi.AddSCIDToIndex(scidstoadd)
							var blTxns *structures.BlockTxns
							blTxns.Topoheight = 1352506
							var h crypto.Hash
							copy(h[:], []byte(line_parts[1])[:])
							blTxns.Tx_hashes = append(blTxns.Tx_hashes, h)
							vi.IndexTxn(blTxns, false)
							if err != nil {
								logger.Printf("Err - %v", err)
							}
						}
					} else {
						logger.Printf("addscid_toindex needs 1 values: single scid to match as arguments\n")
					}
			*/
		case command == "getscidlist_byaddr":
			// TODO: Perhaps add indexer id to a param so you can add it to specific search_filter/indexer. Supported by a 'status' (tbd) command which returns details of each indexer
			if len(line_parts) == 2 && len(line_parts[1]) == 66 {
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					var scidinteracts []string
					switch vi.DBType {
					case "gravdb":
						scidinteracts = vi.GravDBBackend.GetSCIDInteractionByAddr(line_parts[1])
					case "boltdb":
						scidinteracts = vi.BBSBackend.GetSCIDInteractionByAddr(line_parts[1])
					}
					for _, v := range scidinteracts {
						logger.Printf("%v\n", v)
					}
				}
			} else {
				logger.Printf("getscidlist_byaddr needs 1 values: single address to match as arguments\n")
			}
		case command == "pop":
			switch len(line_parts) {
			case 1:
				// Change back 1 height
				for ki, vi := range g.Indexers {
					logger.Printf("- Indexer '%v'", ki)
					if int64(1) > vi.LastIndexedHeight {
						vi.LastIndexedHeight = 1
					} else {
						vi.LastIndexedHeight = vi.LastIndexedHeight - int64(1)
					}
				}
			case 2:
				pop_count := 0
				if s, err := strconv.Atoi(line_parts[1]); err == nil {
					pop_count = s

					// Change back pop_count height
					for ki, vi := range g.Indexers {
						logger.Printf("- Indexer '%v'", ki)
						if int64(pop_count) > vi.LastIndexedHeight {
							vi.LastIndexedHeight = 1
						} else {
							vi.LastIndexedHeight = vi.LastIndexedHeight - int64(pop_count)
						}
					}
				} else {
					logger.Printf("POP needs argument n to pop this many blocks from the top")
				}

			default:
				logger.Printf("POP needs argument n to pop this many blocks from the top")
			}
		case line == "status":
			for ki, vi := range g.Indexers {
				logger.Printf("- Indexer '%v' - Generating status metrics...", ki)
				var validatedSCIDs map[string]string
				var regTxCount, burnTxCount, normTxCount, gnomon_count, scTxCount int64

				switch vi.DBType {
				case "gravdb":
					validatedSCIDs = vi.GravDBBackend.GetAllOwnersAndSCIDs()
					gnomon_count = int64(len(validatedSCIDs))

					regTxCount = vi.GravDBBackend.GetTxCount("registration")
					burnTxCount = vi.GravDBBackend.GetTxCount("burn")
					normTxCount = vi.GravDBBackend.GetTxCount("normal")

					for sc, _ := range validatedSCIDs {
						scTxCount += int64(len(vi.GravDBBackend.GetAllSCIDInvokeDetails(sc)))
					}
				case "boltdb":
					validatedSCIDs = vi.BBSBackend.GetAllOwnersAndSCIDs()
					gnomon_count = int64(len(validatedSCIDs))

					regTxCount = vi.BBSBackend.GetTxCount("registration")
					burnTxCount = vi.BBSBackend.GetTxCount("burn")
					normTxCount = vi.BBSBackend.GetTxCount("normal")

					for sc, _ := range validatedSCIDs {
						scTxCount += int64(len(vi.BBSBackend.GetAllSCIDInvokeDetails(sc)))
					}
				}

				logger.Printf("GNOMON [%d/%d] R:%d >>\n", vi.LastIndexedHeight, vi.ChainHeight, gnomon_count)
				logger.Printf("TXCOUNTS [%d/%d] R:%d B:%d N:%d S:%d >>\n", vi.LastIndexedHeight, vi.ChainHeight, regTxCount, burnTxCount, normTxCount, scTxCount)
				if len(vi.SearchFilter) == 0 {
					logger.Printf("SEARCHFILTER(S) [%d/%d] >> %s\n", vi.LastIndexedHeight, vi.ChainHeight, "ALL SCs")
				} else {
					logger.Printf("SEARCHFILTER(S) [%d/%d] >> %s\n", vi.LastIndexedHeight, vi.ChainHeight, strings.Join(vi.SearchFilter, ";;;"))
				}
			}
		case line == "gnomonsc":
			logger.Printf("[Mainnet] %s", structures.MAINNET_GNOMON_SCID)
			logger.Printf("[Testnet] %s", structures.TESTNET_GNOMON_SCID)
		case line == "quit":
			logger.Printf("'quit' received, putting gnomes to sleep. This will take ~5sec.\n")
			g.Close()
			return nil
		case line == "bye":
			logger.Printf("'bye' received, putting gnomes to sleep. This will take ~5sec.\n")
			g.Close()
			return nil
		case line == "exit":
			logger.Printf("'exit' received, putting gnomes to sleep. This will take ~5sec.\n")
			g.Close()
			return nil
		default:
			logger.Printf("You said: %v\n", strconv.Quote(line))
		}
	}
}

func usage(w io.Writer) {
	io.WriteString(w, "commands:\n")
	io.WriteString(w, "\t\033[1mhelp\033[0m\t\tthis help\n")
	io.WriteString(w, "\t\033[1mversion\033[0m\t\tShow gnomon version\n")
	io.WriteString(w, "\t\033[1mlistsc\033[0m\t\tLists all indexed scids that match original search filter\n")
	io.WriteString(w, "\t\033[1mlistsc_code\033[0m\t\tLists SCID code, listsc_code <scid>\n")
	io.WriteString(w, "\t\033[1mlistsc_variables\033[0m\t\tLists SCID variables at latest height unless optionally defining a height, listsc_variables <scid> <height>\n")
	//io.WriteString(w, "\t\033[1mnew_sf\033[0m\t\tStarts a new gnomon search (to be deprecated/modified), new_sf <searchfilterstring>\n")
	io.WriteString(w, "\t\033[1mlistsc_byowner\033[0m\tLists SCIDs by owner, listsc_byowner <owneraddress>\n")
	io.WriteString(w, "\t\033[1mlistsc_byscid\033[0m\tList a scid/owner pair by scid and optionally at a specified height and higher, listsc_byscid <scid> <minheight>\n")
	io.WriteString(w, "\t\033[1mlistsc_byheight\033[0m\tList all indexed scids that match original search filter including height deployed, listsc_byheight\n")
	io.WriteString(w, "\t\033[1mlistsc_balances\033[0m\tLists balances of SCIDs that are greater than 0, listsc_balances\n")
	io.WriteString(w, "\t\033[1mlistsc_byentrypoint\033[0m\tLists sc invokes by entrypoint, listsc_byentrypoint <scid> <entrypoint>\n")
	io.WriteString(w, "\t\033[1mlistsc_byinitialize\033[0m\tLists all calls to SCs that attempted to run Initialize or InitializePrivate()\n")
	io.WriteString(w, "\t\033[1mlistscinvoke_bysigner\033[0m\tLists all sc invokes that match a given signer or partial signer address and optionally by scid, listscinvoke_bysigner <signerstring> || listscinvoke_bysigner <signerstring> <scid>\n")
	io.WriteString(w, "\t\033[1mlistscidkey_byvaluestored\033[0m\tList keys in a SC that match a given value by pulling from gnomon database, listscidkey_byvaluestored <scid> <value>\n")
	io.WriteString(w, "\t\033[1mlistscidkey_byvaluelive\033[0m\tList keys in a SC that match a given value by pulling from daemon, listscidkey_byvaluelive <scid> <value>\n")
	io.WriteString(w, "\t\033[1mlistscidvalue_bykeystored\033[0m\tList keys in a SC that match a given value by pulling from gnomon database, listscidvalue_bykeystored <scid> <key>\n")
	io.WriteString(w, "\t\033[1mlistscidvalue_bykeylive\033[0m\tList keys in a SC that match a given value by pulling from daemon, listscidvalue_bykeylive <scid> <key>\n")
	io.WriteString(w, "\t\033[1mvalidatesc\033[0m\tValidates a SC looking for a 'signature' k/v pair containing DERO signature validating the code matches the signature, validatesc <scid>\n")
	io.WriteString(w, "\t\033[1maddscid_toindex\033[0m\tAdd a SCID to index list/validation filter manually, addscid_toindex <scid>\n")
	//io.WriteString(w, "\t\033[1mindex_txn\033[0m\tIndex a specific txid (alpha), addscid_toindex <scid>\n")
	io.WriteString(w, "\t\033[1mgetscidlist_byaddr\033[0m\tGets list of scids that addr has interacted with, getscidlist_byaddr <addr>\n")
	io.WriteString(w, "\t\033[1mpop\033[0m\tRolls back lastindexheight, pop <100>\n")
	io.WriteString(w, "\t\033[1mstatus\033[0m\t\tShow general information\n")
	io.WriteString(w, "\t\033[1mgnomonsc\033[0m\t\tShow scid of gnomon index scs\n")

	io.WriteString(w, "\t\033[1mbye\033[0m\t\tQuit the daemon\n")
	io.WriteString(w, "\t\033[1mexit\033[0m\t\tQuit the daemon\n")
	io.WriteString(w, "\t\033[1mquit\033[0m\t\tQuit the daemon\n")
}

func (g *GnomonServer) Close() {
	g.Closing = true

	for _, v := range g.Indexers {
		go v.Close()
	}

	time.Sleep(time.Second * 5)

	os.Exit(0)
}
