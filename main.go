package main

import (
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chzyer/readline"
	"github.com/civilware/Gnomon/api"
	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"
	"github.com/deroproject/derohe/globals"
	"github.com/docopt/docopt-go"
)

type GnomonServer struct {
	LastIndexedHeight int64
	SearchFilters     []string
	Indexers          map[string]*indexer.Indexer
}

var command_line string = `Gnomon
Gnomon Indexing Service: Index DERO's blockchain for Artificer NFT deployments/listings/etc.

Usage:
  gnomon [options]
  gnomon -h | --help

Options:
  -h --help     Show this screen.
  --daemon-rpc-address=<127.0.0.1:40402>	connect to daemon
  --api-address=<127.0.0.1:8082>	host api
  --enable-api-ssl=<false>	enable ssl. Either true/false
  --api-ssl-address=127.0.0.1:9092>		host ssl api
  --start-topoheight=<31170>	define a start topoheight other than 1 if required to index at a higher block (pruned db etc.)
  --search-filter=<"Function InputStr(input String, varname String) Uint64">	defines a search filter to match on installed SCs to add to validated list and index all actions, this will most likely change in the future but can allow for some small variability. Include escapes etc. if required. If nothing is defined, it will pull all (minus hardcoded sc)`

var Exit_In_Progress = make(chan bool)

var daemon_endpoint string
var api_endpoint string
var api_ssl_endpoint string
var sslenabled bool
var Connected bool = false
var Closing bool = false
var search_filter string

var RLI *readline.Instance

var gnomon_count int64

var Gnomon = &GnomonServer{}

func main() {
	var err error

	n := runtime.NumCPU()
	runtime.GOMAXPROCS(n)

	globals.Initialize()

	Gnomon.Indexers = make(map[string]*indexer.Indexer)

	// Inspect argument(s)
	var arguments map[string]interface{}
	arguments, err = docopt.Parse(command_line, nil, true, "Gnomon : work in progress", false)

	if err != nil {
		log.Fatalf("[Main] Error while parsing arguments err: %s\n", err)
	}

	// Set variables from arguments
	daemon_endpoint = "127.0.0.1:40402"
	if arguments["--daemon-rpc-address"] != nil {
		daemon_endpoint = arguments["--daemon-rpc-address"].(string)
	}

	log.Printf("[Main] Using daemon RPC endpoint %s\n", daemon_endpoint)

	api_endpoint = "127.0.0.1:8082"
	if arguments["--api-address"] != nil {
		api_endpoint = arguments["--api-address"].(string)
	}

	api_ssl_endpoint = "127.0.0.1:9092"
	if arguments["--api-ssl-address"] != nil {
		api_ssl_endpoint = arguments["--api-ssl-address"].(string)
	}

	if arguments["--enable-api-ssl"] != nil {
		sslenabled = arguments["--enable-api-ssl"].(bool)
		log.Printf("Setting API SSL to enabled\n")
	}

	last_indexedheight := int64(1)
	if arguments["--start-topoheight"] != nil {
		last_indexedheight, err = strconv.ParseInt(arguments["--start-topoheight"].(string), 10, 64)
		if err != nil {
			log.Fatalf("[Main] ERROR while converting --start-topoheight to int64\n")
		}
	}

	if arguments["--search-filter"] != nil {
		search_filter = arguments["--search-filter"].(string)
		log.Printf("[Main] Using search filter: %v\n", search_filter)
	} else {
		log.Printf("[Main] No search filter defined.. grabbing all.\n")
	}

	// Database
	shasum := fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
	db_folder := fmt.Sprintf("%s_%s", "GNOMON", shasum)
	Graviton_backend := storage.NewGravDB(db_folder, "25ms")

	// API
	apic := &structures.APIConfig{
		Enabled:              true,
		Listen:               api_endpoint,
		StatsCollectInterval: "5s",
		SSL:                  sslenabled,
		SSLListen:            api_ssl_endpoint,
		CertFile:             "fullchain.cer",
		KeyFile:              "cert.key",
	}
	// TODO: Add default search filter index of sorts, rather than passing through Graviton_backend object as a whole
	apis := api.NewApiServer(apic, Graviton_backend)
	go apis.Start()

	// Start default indexer based on search_filter params
	defaultIndexer := indexer.NewIndexer(Graviton_backend, search_filter, last_indexedheight, daemon_endpoint)
	go defaultIndexer.Start()
	Gnomon.Indexers[search_filter] = defaultIndexer

	// Probably not the best way to do it... should branch it out to be an accessible Closing call/function for all in some capacity (probably Gnomon.Indexers[] selection of sorts)
	go func() {
		for {
			if Closing == true {
				defaultIndexer.Closing = true
			}
		}
	}()

	// Setup ctrl+c exit
	SetupCloseHandler(Graviton_backend, defaultIndexer)

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
			if err = readline_loop(RLI, Graviton_backend); err == nil {
				break
			}
		}
	}()

	// This tiny goroutine continuously updates status as required
	go func() {
		for {
			select {
			case <-Exit_In_Progress:
				Closing = true
				return
			default:
			}

			validatedSCIDs := Graviton_backend.GetAllOwnersAndSCIDs()
			gnomon_count = int64(len(validatedSCIDs))

			currheight := defaultIndexer.LastIndexedHeight - 1

			// choose color based on urgency
			color := "\033[32m" // default is green color
			if currheight < defaultIndexer.ChainTopoHeight {
				color = "\033[33m" // make prompt yellow
			} else if currheight > defaultIndexer.ChainTopoHeight {
				color = "\033[31m" // make prompt red
			}

			gcolor := "\033[32m" // default is green color
			if gnomon_count < 1 {
				gcolor = "\033[33m" // make prompt yellow
			}

			RLI.SetPrompt(fmt.Sprintf("\033[1m\033[32mGNOMON \033[0m"+color+"[%d/%d] "+gcolor+"R:%d G:%d >>\033[0m ", currheight, defaultIndexer.ChainTopoHeight, gnomon_count, len(Gnomon.Indexers)))
			RLI.Refresh()
			time.Sleep(1 * time.Second)
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

func readline_loop(l *readline.Instance, Graviton_backend *storage.GravitonStore) (err error) {

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
				log.Printf("Ctrl-C received, ending loop. Hit it again to exit the program (This will be fixed..)\n")
				Closing = true
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

		switch {
		case command == "listsc":
			sclist := Graviton_backend.GetAllOwnersAndSCIDs()
			for k, v := range sclist {
				log.Printf("SCID: %v ; Owner: %v\n", k, v)
			}
		case command == "listsc_byowner":
			if len(line_parts) == 2 && len(line_parts[1]) == 66 {
				sclist := Graviton_backend.GetAllOwnersAndSCIDs()
				var count int64
				for k, v := range sclist {
					if v == line_parts[1] {
						log.Printf("SCID: %v ; Owner: %v\n", k, v)
						invokedetails := Graviton_backend.GetAllSCIDInvokeDetails(k)
						for _, invoke := range invokedetails {
							log.Printf("%v", invoke)
						}
						count++
					}
				}

				if count == 0 {
					log.Printf("No SCIDs installed by %v\n", line_parts[1])
				}
			} else {
				log.Printf("listsc_byowner needs a single owner address as argument\n")
			}
		case command == "listsc_byscid":
			if len(line_parts) == 2 && len(line_parts[1]) == 64 {
				sclist := Graviton_backend.GetAllOwnersAndSCIDs()
				var count int64
				for k, v := range sclist {
					if k == line_parts[1] {
						log.Printf("SCID: %v ; Owner: %v\n", k, v)
						invokedetails := Graviton_backend.GetAllSCIDInvokeDetails(k)
						for _, invoke := range invokedetails {
							log.Printf("%v\n", invoke)
						}
						count++
					}
				}

				if count == 0 {
					log.Printf("No SCIDs installed matching %v\n", line_parts[1])
				}
			} else {
				log.Printf("listsc_byscid needs a single scid as argument\n")
			}
		default:
			log.Printf("You said: %v\n", strconv.Quote(line))
		}
	}

	//return fmt.Errorf("can never reach here")
}

// SetupCloseHandler creates a 'listener' on a new goroutine which will notify the
// program if it receives an interrupt from the OS. We then handle this by calling
// our clean up procedure and exiting the program.
// Reference: https://golangcode.com/handle-ctrl-c-exit-in-terminal/
func SetupCloseHandler(Graviton_backend *storage.GravitonStore, defaultIndexer *indexer.Indexer) {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Printf("\r- Ctrl+C pressed in Terminal\n")
		log.Printf("[SetupCloseHandler] Closing - syncing stats...\n")
		Closing = true

		time.Sleep(time.Second)

		// Log the last_indexedheight
		err := Graviton_backend.StoreLastIndexHeight(defaultIndexer.LastIndexedHeight)
		if err != nil {
			log.Printf("[SetupCloseHandler] ERR - Erorr storing last index height: %v\n", err)
		}

		// Add 1 second sleep prior to closing to prevent db writing issues
		time.Sleep(time.Second)
		Graviton_backend.DB.Close()
		os.Exit(0)
	}()
}
