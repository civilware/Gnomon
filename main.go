package main

import (
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/civilware/Gnomon/api"
	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/mbllookup"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"
	"github.com/docopt/docopt-go"
)

type GnomonServer struct {
	LastIndexedHeight int64
	SearchFilters     []string
	Indexers          map[string]*indexer.Indexer
	Closing           bool
	DaemonEndpoint    string
	RunMode           string
	MBLLookup         bool
}

var command_line string = `Gnomon
Gnomon Indexing Service: Index DERO's blockchain for Smart Contract deployments/listings/etc. as well as other data analysis.

Usage:
  gnomon [options]
  gnomon -h | --help

Options:
  -h --help     Show this screen.
  --daemon-rpc-address=<127.0.0.1:40402>	Connect to daemon.
  --api-address=<127.0.0.1:8082>	Host api.
  --enable-api-ssl=<false>	Enable ssl.
  --api-ssl-address=<127.0.0.1:9092>		Host ssl api.
  --get-info-ssl-address=<127.0.0.1:9394>	Host GetInfo ssl api. This is to completely isolate it from gnomon api results as a whole. Normal api endpoints also surface the getinfo call if needed.
  --start-topoheight=<31170>	Define a start topoheight other than 1 if required to index at a higher block (pruned db etc.).
  --search-filter=<"Function InputStr(input String, varname String) Uint64">	Defines a search filter to match on installed SCs to add to validated list and index all actions, this will most likely change in the future but can allow for some small variability. Include escapes etc. if required. If nothing is defined, it will pull all (minus hardcoded sc).
  --runmode=<daemon>	Defines the runmode of gnomon (daemon/wallet). By default this is daemon mode which indexes directly from the chain. Wallet mode indexes from wallet tx history (use/store with caution).
  --enable-miniblock-lookup=<false>	True/false value to store all miniblocks and their respective details and miner addresses who found them. This currently REQUIRES a full node db in same directory
  --close-on-disconnect=<false>	True/false value to close out indexers in the event of daemon disconnect. Daemon will fail connections for 30 seconds and then close the indexer. This is for HA pairs or wanting services off on disconnect.
  --fastsync	True/false value to define loading at chain height and only keeping track of list of SCIDs and their respective up-to-date variable stores as it hits them. NOTE: You will not get all information and may rely on manual scid additions.`

var Exit_In_Progress = make(chan bool)

var daemon_endpoint string
var api_endpoint string
var api_ssl_endpoint string
var get_info_ssl_endpoint string
var sslenabled bool
var closeondisconnect bool
var fastsync bool
var search_filter string
var mbl bool
var version = "0.1a"

var RLI *readline.Instance

var Gnomon = &GnomonServer{}

func main() {
	var err error

	n := runtime.NumCPU()
	runtime.GOMAXPROCS(n)

	//globals.Initialize()

	Gnomon.Indexers = make(map[string]*indexer.Indexer)

	// Inspect argument(s)
	arguments, err := docopt.ParseArgs(command_line, nil, version)

	if err != nil {
		log.Fatalf("[Main] Error while parsing arguments err: %s\n", err)
	}

	// Set variables from arguments
	daemon_endpoint = "127.0.0.1:40402"
	if arguments["--daemon-rpc-address"] != nil {
		daemon_endpoint = arguments["--daemon-rpc-address"].(string)
	}
	Gnomon.DaemonEndpoint = daemon_endpoint

	log.Printf("[Main] Using daemon RPC endpoint %s\n", daemon_endpoint)

	api_endpoint = "127.0.0.1:8082"
	if arguments["--api-address"] != nil {
		api_endpoint = arguments["--api-address"].(string)
	}

	api_ssl_endpoint = "127.0.0.1:9092"
	if arguments["--api-ssl-address"] != nil {
		api_ssl_endpoint = arguments["--api-ssl-address"].(string)
	}

	get_info_ssl_endpoint = "127.0.0.1:9394"
	if arguments["--get-info-ssl-address"] != nil {
		get_info_ssl_endpoint = arguments["--get-info-ssl-address"].(string)
	}

	if arguments["--enable-api-ssl"] != nil {
		sslenablestr := arguments["--enable-api-ssl"].(string)
		if sslenablestr == "true" {
			sslenabled = true
		}
	}

	Gnomon.RunMode = "daemon"
	if arguments["--runmode"] != nil {
		if arguments["--runmode"] == "daemon" || arguments["--runmode"] == "wallet" {
			Gnomon.RunMode = arguments["--runmode"].(string)
		} else {
			log.Fatalf("ERR - Runmode must be either 'daemon' or 'wallet'")
			return
		}
	}

	last_indexedheight := int64(1)
	if arguments["--start-topoheight"] != nil {
		last_indexedheight, err = strconv.ParseInt(arguments["--start-topoheight"].(string), 10, 64)
		if err != nil {
			log.Fatalf("[Main] ERROR while converting --start-topoheight to int64\n")
			return
		}
	}

	if arguments["--search-filter"] != nil {
		search_filter = arguments["--search-filter"].(string)
		log.Printf("[Main] Using search filter: %v\n", search_filter)
	} else {
		log.Printf("[Main] No search filter defined.. grabbing all.\n")
	}

	if arguments["--enable-miniblock-lookup"] != nil {
		mbllookupstr := arguments["--enable-miniblock-lookup"].(string)
		if mbllookupstr == "true" {
			mbl = true
		}

		err = mbllookup.DeroDB.LoadDeroDB()
		if err != nil {
			log.Fatalf("[Main] ERR Loading DeroDB - Be sure to run from directory of fully synced mainnet - %v\n", err)
			return
		}
	}
	Gnomon.MBLLookup = mbl

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

	// Database
	var shasum string
	if search_filter == "" {
		shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
	} else {
		shasum = fmt.Sprintf("%x", sha1.Sum([]byte(search_filter)))
	}
	db_folder := fmt.Sprintf("gnomondb\\%s_%s", "GNOMON", shasum)
	Graviton_backend := storage.NewGravDB(db_folder, "25ms")

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
	apis := api.NewApiServer(apic, Graviton_backend)
	go apis.Start()

	// Start default indexer based on search_filter params
	defaultIndexer := indexer.NewIndexer(Graviton_backend, search_filter, last_indexedheight, daemon_endpoint, Gnomon.RunMode, mbl, closeondisconnect, fastsync)

	switch Gnomon.RunMode {
	case "daemon":
		go defaultIndexer.StartDaemonMode()
	default:
		go defaultIndexer.StartDaemonMode()
	}
	Gnomon.Indexers[search_filter] = defaultIndexer

	// Setup ctrl+c exit
	//SetupCloseHandler(Graviton_backend, defaultIndexer)

	// Readline GNOMON
	RLI, err = readline.NewEx(&readline.Config{
		//Prompt:          "\033[92mGNOMON:\033[32m»\033[0m",
		Prompt:      "\033[92mGNOMON\033[32m>>>\033[0m ",
		HistoryFile: filepath.Join(os.TempDir(), "derod_readline.tmp"),
		//AutoComplete:    completer,
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

			validatedSCIDs := Graviton_backend.GetAllOwnersAndSCIDs()
			gnomon_count := int64(len(validatedSCIDs))

			currheight := defaultIndexer.LastIndexedHeight - 1

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
			log.Printf("Readline_loop err: %v\n", err)
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
				log.Printf("Ctrl-C received, putting gnomes to sleep. This will take ~5sec.\n")
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
			log.Printf("Version: %v", version)
		case command == "listsc":
			for ki, vi := range g.Indexers {
				log.Printf("- Indexer '%v'", ki)
				sclist := vi.Backend.GetAllOwnersAndSCIDs()
				for k, v := range sclist {
					log.Printf("SCID: %v ; Owner: %v\n", k, v)
				}
			}
		case command == "new_sf":
			if len(line_parts) >= 2 {
				nsf := strings.Join(line_parts[1:], " ")
				log.Printf("Adding new searchfilter '%v'\n", nsf)

				// Database
				nShasum := fmt.Sprintf("%x", sha1.Sum([]byte(nsf)))
				nDBFolder := fmt.Sprintf("gnomondb\\%s_%s", "GNOMON", nShasum)
				log.Printf("Adding new database '%v'\n", nDBFolder)
				nBackend := storage.NewGravDB(nDBFolder, "25ms")

				// Start default indexer based on search_filter params
				log.Printf("Adding new indexer. ID: '%v'; - SearchFilter: '%v'\n", len(g.Indexers)+1, nsf)
				nIndexer := indexer.NewIndexer(nBackend, nsf, 0, g.DaemonEndpoint, g.RunMode, g.MBLLookup, closeondisconnect, fastsync)
				go nIndexer.StartDaemonMode()
				g.Indexers[nsf] = nIndexer
			}
		case command == "listsc_byowner":
			if len(line_parts) == 2 && len(line_parts[1]) == 66 {
				for ki, vi := range g.Indexers {
					log.Printf("- Indexer '%v'", ki)
					sclist := vi.Backend.GetAllOwnersAndSCIDs()
					var count int64
					for k, v := range sclist {
						if v == line_parts[1] {
							log.Printf("SCID: %v ; Owner: %v\n", k, v)
							invokedetails := vi.Backend.GetAllSCIDInvokeDetails(k)
							for _, invoke := range invokedetails {
								log.Printf("%v", invoke)
							}
							count++
						}
					}

					if count == 0 {
						log.Printf("No SCIDs installed by %v\n", line_parts[1])
					}
				}
			} else {
				log.Printf("listsc_byowner needs a single owner address as argument\n")
			}
		case command == "listsc_byscid":
			if len(line_parts) == 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					log.Printf("- Indexer '%v'", ki)
					sclist := vi.Backend.GetAllOwnersAndSCIDs()
					var count int64
					for k, v := range sclist {
						if k == line_parts[1] {
							log.Printf("SCID: %v ; Owner: %v\n", k, v)
							invokedetails := vi.Backend.GetAllSCIDInvokeDetails(k)
							for _, invoke := range invokedetails {
								log.Printf("%v\n", invoke)
							}
							count++
						}
					}

					if count == 0 {
						log.Printf("No SCIDs installed matching %v\n", line_parts[1])
					}
				}
			} else {
				log.Printf("listsc_byscid needs a single scid as argument\n")
			}
		case command == "listscidkey_byvalue":
			if len(line_parts) == 3 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					log.Printf("- Indexer '%v'", ki)
					sclist := vi.Backend.GetAllOwnersAndSCIDs()
					var count int64
					for k, v := range sclist {
						if k == line_parts[1] {
							log.Printf("SCID: %v ; Owner: %v\n", k, v)
							keysstringbyvalue, keysuint64byvalue := vi.Backend.GetSCIDKeysByValue(k, line_parts[2], vi.ChainHeight)
							for _, skey := range keysstringbyvalue {
								log.Printf("%v\n", skey)
							}
							for _, ukey := range keysuint64byvalue {
								log.Printf("%v\n", ukey)
							}
							count++
						}
					}

					if count == 0 {
						log.Printf("No SCIDs installed matching %v\n", line_parts[1])
					}
				}
			} else {
				log.Printf("listscidkey_byvalue needs two values: single scid and value to match as arguments\n")
			}
		case command == "addscid_toindex":
			// TODO: Perhaps add indexer id to a param so you can add it to specific search_filter/indexer. Supported by a 'status' (tbd) command which returns details of each indexer
			if len(line_parts) == 2 && len(line_parts[1]) == 64 {
				for ki, vi := range g.Indexers {
					log.Printf("- Indexer '%v'", ki)
					var scl []string
					scl = append(scl, line_parts[1])
					err = vi.AddSCIDToIndex(scl)
					if err != nil {
						log.Printf("Err - %v", err)
					}
				}
			} else {
				log.Printf("addscid_toindex needs 1 values: single scid to match as arguments\n")
			}
		case command == "getscidlist_byaddr":
			// TODO: Perhaps add indexer id to a param so you can add it to specific search_filter/indexer. Supported by a 'status' (tbd) command which returns details of each indexer
			if len(line_parts) == 2 && len(line_parts[1]) == 66 {
				for ki, vi := range g.Indexers {
					log.Printf("- Indexer '%v'", ki)
					scidinteracts := vi.Backend.GetSCIDInteractionByAddr(line_parts[1])
					for _, v := range scidinteracts {
						log.Printf("%v\n", v)
					}
				}
			} else {
				log.Printf("getscidlist_byaddr needs 1 values: single address to match as arguments\n")
			}
		case command == "pop":
			switch len(line_parts) {
			case 1:
				// Change back 1 height
				for ki, vi := range g.Indexers {
					log.Printf("- Indexer '%v'", ki)
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
						log.Printf("- Indexer '%v'", ki)
						if int64(pop_count) > vi.LastIndexedHeight {
							vi.LastIndexedHeight = 1
						} else {
							vi.LastIndexedHeight = vi.LastIndexedHeight - int64(pop_count)
						}
					}
				} else {
					log.Printf("POP needs argument n to pop this many blocks from the top")
				}

			default:
				log.Printf("POP needs argument n to pop this many blocks from the top")
			}
		case line == "status":
			for ki, vi := range g.Indexers {
				log.Printf("- Indexer '%v'", ki)
				validatedSCIDs := vi.Backend.GetAllOwnersAndSCIDs()
				gnomon_count := int64(len(validatedSCIDs))
				currheight := vi.LastIndexedHeight - 1

				log.Printf("GNOMON [%d/%d] R:%d >>", currheight, vi.ChainHeight, gnomon_count)
			}
			/*
				case line == "change":
					for ki, vi := range g.Indexers {
						log.Printf("- Indexer '%v'", ki)
						log.Printf("Old endpoint - %v", vi.Endpoint)
						vi.Lock()
						if vi.Endpoint == "127.0.0.1:10102" {
							vi.Endpoint = "127.0.0.1:40402"
						} else {
							vi.Endpoint = "127.0.0.1:10102"
						}
						log.Printf("New endpoint - %v", vi.Endpoint)
						vi.Unlock()
					}
			*/
		case line == "quit":
			log.Printf("'quit' received, putting gnomes to sleep. This will take ~5sec.\n")
			g.Close()
			return nil
		case line == "bye":
			log.Printf("'bye' received, putting gnomes to sleep. This will take ~5sec.\n")
			g.Close()
			return nil
		case line == "exit":
			log.Printf("'exit' received, putting gnomes to sleep. This will take ~5sec.\n")
			g.Close()
			return nil
		default:
			log.Printf("You said: %v\n", strconv.Quote(line))
		}
	}

	//return fmt.Errorf("can never reach here")
}

func usage(w io.Writer) {
	io.WriteString(w, "commands:\n")
	io.WriteString(w, "\t\033[1mhelp\033[0m\t\tthis help\n")
	io.WriteString(w, "\t\033[1mversion\033[0m\t\tShow gnomon version\n")
	io.WriteString(w, "\t\033[1mlistsc\033[0m\t\tLists all indexed scids that match original search filter\n")
	io.WriteString(w, "\t\033[1mnew_sf\033[0m\t\tStarts a new gnomon search, new_sf <searchfilterstring>\n")
	io.WriteString(w, "\t\033[1mlistsc_byowner\033[0m\tLists SCIDs by owner, listsc_byowner <owneraddress>\n")
	io.WriteString(w, "\t\033[1mlistsc_byscid\033[0m\tList a scid/owner pair by scid, listsc_byscid <scid>\n")
	io.WriteString(w, "\t\033[1mlistscidkey_byvalue\033[0m\tList keys in a SC that match a given value, listscidkey_byvalue <scid> <value>\n")
	io.WriteString(w, "\t\033[1maddscid_toindex\033[0m\tAdd a SCID to index list/validation filter manually, addscid_toindex <scid>\n")
	io.WriteString(w, "\t\033[1mgetscidlist_byaddr\033[0m\tGets list of scids that addr has interacted with, getscidlist_byaddr <addr>\n")
	io.WriteString(w, "\t\033[1mstatus\033[0m\t\tShow general information\n")

	io.WriteString(w, "\t\033[1mbye\033[0m\t\tQuit the daemon\n")
	io.WriteString(w, "\t\033[1mexit\033[0m\t\tQuit the daemon\n")
	io.WriteString(w, "\t\033[1mquit\033[0m\t\tQuit the daemon\n")
}

func (g *GnomonServer) Close() {
	g.Closing = true
	/*
		for _, v := range g.Indexers {
			v.Closing = true
		}

		time.Sleep(time.Second * 5)

		for _, v := range g.Indexers {
			v.Backend.DB.Close()
		}
	*/

	for _, v := range g.Indexers {
		go v.Close()
	}

	time.Sleep(time.Second * 5)

	os.Exit(0)
}

// SetupCloseHandler creates a 'listener' on a new goroutine which will notify the
// program if it receives an interrupt from the OS. We then handle this by calling
// our clean up procedure and exiting the program.
// Reference: https://golangcode.com/handle-ctrl-c-exit-in-terminal/
/*
func SetupCloseHandler(Graviton_backend *storage.GravitonStore, defaultIndexer *indexer.Indexer) {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Printf("\r- Ctrl+C pressed in Terminal\n")
		log.Printf("[SetupCloseHandler] Closing - syncing stats...\n")

		Gnomon.close()

		time.Sleep(time.Second)

		// Add 1 second sleep prior to closing to prevent db writing issues
		time.Sleep(time.Second)
		Graviton_backend.DB.Close()
		os.Exit(0)
	}()
}
*/
