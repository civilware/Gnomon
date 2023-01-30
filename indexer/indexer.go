package indexer

import (
	"context"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/civilware/Gnomon/mbllookup"
	"github.com/civilware/Gnomon/rwc"
	"github.com/civilware/Gnomon/storage"
	"github.com/civilware/Gnomon/structures"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/cryptography/bn256"
	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/deroproject/graviton"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/gorilla/websocket"
)

type Client struct {
	WS  *websocket.Conn
	RPC *jrpc2.Client
	sync.RWMutex
}

type SCIDToIndexStage struct {
	scid     string
	fsi      *structures.FastSyncImport
	scVars   []*structures.SCIDVariable
	scCode   string
	contains bool
}

type Indexer struct {
	LastIndexedHeight int64
	ChainHeight       int64
	SearchFilter      []string
	Backend           *storage.GravitonStore
	Closing           bool
	RPC               *Client
	Endpoint          string
	RunMode           string
	MBLLookup         bool
	ValidatedSCs      []string
	CloseOnDisconnect bool
	Fastsync          bool
	sync.RWMutex
}

// Defines the number of blocks to jump when testing pruned nodes.
const block_jump = int64(10000)

// After daemon connection will check if mainnet/testnet and adjust accordingly
const mainnet_gnomon_scid = "a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4"
const testnet_gnomon_scid = "c9d23d2fc3aaa8e54e238a2218c0e5176a6e48780920fd8474fac5b0576110a2"

// String set of hardcoded scids which are appended to in NewIndexer. These are used for reference points such as ignoring invoke calls for indexer.Fastsync == true among other procedures.
var hardcodedscids []string

var Connected bool = false

func NewIndexer(Graviton_backend *storage.GravitonStore, search_filter []string, last_indexedheight int64, endpoint string, runmode string, mbllookup bool, closeondisconnect bool, fastsync bool) *Indexer {
	hardcodedscids = append(hardcodedscids, "0000000000000000000000000000000000000000000000000000000000000001")

	return &Indexer{
		LastIndexedHeight: last_indexedheight,
		SearchFilter:      search_filter,
		Backend:           Graviton_backend,
		RPC:               &Client{},
		Endpoint:          endpoint,
		RunMode:           runmode,
		MBLLookup:         mbllookup,
		CloseOnDisconnect: closeondisconnect,
		Fastsync:          fastsync,
	}
}

func (indexer *Indexer) StartDaemonMode(blockParallelNum int) {
	var err error

	// Simple connect loop .. if connection fails initially then keep trying, else break out and continue on. Connect() is handled in getInfo() for retries later on if connection ceases again
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		log.Printf("[StartDaemonMode] Trying to connect...")
		err = indexer.RPC.Connect(indexer.Endpoint)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}
		break
	}
	time.Sleep(1 * time.Second)

	// Continuously getInfo from daemon to update topoheight globally
	go indexer.getInfo()
	time.Sleep(1 * time.Second)

	storedindex := indexer.Backend.GetLastIndexHeight()

	// If storedindex returns 0, first opening, and fastsync is enabled set index to current chain height
	if storedindex == 0 && indexer.Fastsync {
		log.Printf("[StartDaemonMode] Fastsync initiated, setting to chainheight (%v)", indexer.ChainHeight)
		storedindex = indexer.ChainHeight
	}

	for _, vi := range hardcodedscids {
		if scidExist(indexer.ValidatedSCs, vi) {
			// Hardcoded SCID already exists, no need to re-add
			continue
		}

		scVars, scCode, _ := indexer.RPC.GetSCVariables(vi, indexer.ChainHeight)

		var contains bool

		// If we can get the SC and searchfilter is "" (get all), contains is true. Otherwise evaluate code against searchfilter
		if len(indexer.SearchFilter) == 0 {
			contains = true
		} else {
			// Ensure scCode is not blank (e.g. an invalid scid)
			if scCode != "" {
				for _, sfv := range indexer.SearchFilter {
					contains = strings.Contains(scCode, sfv)
					if contains {
						// Break b/c we want to ensure contains remains true. Only care if it matches at least 1 case
						break
					}
				}
			}
		}

		if contains {
			//log.Printf("[AddSCIDToIndex] Hardcoded SCID matches search filter. Adding SCID %v", vi)
			indexer.Lock()
			indexer.ValidatedSCs = append(indexer.ValidatedSCs, vi)
			indexer.Unlock()
			writeWait, _ := time.ParseDuration("50ms")
			for indexer.Backend.Writing == 1 {
				if indexer.Closing {
					return
				}
				//log.Printf("[Indexer-NewIndexer] GravitonDB is writing... sleeping for %v...", writeWait)
				time.Sleep(writeWait)
			}
			indexer.Backend.Writing = 1
			var ctrees []*graviton.Tree
			sotree, sochanges, err := indexer.Backend.StoreOwner(vi, "", true)
			if err != nil {
				log.Printf("[StartDaemonMode-hardcodedscids] Error storing owner: %v\n", err)
			} else {
				if sochanges {
					ctrees = append(ctrees, sotree)
				}
			}
			svdtree, svdchanges, err := indexer.Backend.StoreSCIDVariableDetails(vi, scVars, indexer.ChainHeight, true)
			if err != nil {
				log.Printf("[StartDaemonMode-hardcodedscids] ERR - storing scid variable details: %v\n", err)
			} else {
				if svdchanges {
					ctrees = append(ctrees, svdtree)
				}
			}
			sihtree, sihchanges, err := indexer.Backend.StoreSCIDInteractionHeight(vi, indexer.ChainHeight, true)
			if err != nil {
				log.Printf("[StartDaemonMode-hardcodedscids] ERR - storing scid interaction height: %v\n", err)
			} else {
				if sihchanges {
					ctrees = append(ctrees, sihtree)
				}
			}
			if len(ctrees) > 0 {
				_, err := indexer.Backend.CommitTrees(ctrees)
				if err != nil {
					log.Printf("[StartDaemonMode-hardcodedscids] ERR - committing trees: %v\n", err)
				} else {
					//log.Printf("[StartDaemonMode-hardcodedscids] DEBUG - cv [%v]\n", cv)
				}
			}
			indexer.Backend.Writing = 0
		}
	}

	if storedindex > indexer.LastIndexedHeight {
		log.Printf("[StartDaemonMode-storedIndex] Continuing from last indexed height %v\n", storedindex)
		indexer.Lock()
		indexer.LastIndexedHeight = storedindex
		indexer.Unlock()

		// We can also assume this check to mean we have stored validated SCs potentially. TODO: Do we just get stored SCs regardless of sync cycle?
		pre_validatedSCIDs := indexer.Backend.GetAllOwnersAndSCIDs()

		if len(pre_validatedSCIDs) > 0 {
			log.Printf("[StartDaemonMode] Appending pre-validated SCIDs from store to memory.\n")

			for k := range pre_validatedSCIDs {
				indexer.Lock()
				indexer.ValidatedSCs = append(indexer.ValidatedSCs, k)
				indexer.Unlock()
			}
		}

		getinfo := indexer.Backend.GetGetInfoDetails()
		if getinfo != nil {
			// Define gnomon builtin scid for indexing
			var gnomon_scid string
			if !getinfo.Testnet {
				gnomon_scid = mainnet_gnomon_scid
			} else {
				gnomon_scid = testnet_gnomon_scid
			}

			// All could be future optimized .. for now it's slower but works.
			variables, code, _ := indexer.RPC.GetSCVariables(gnomon_scid, indexer.ChainHeight)
			if len(variables) > 0 {
				_ = code
				keysstring, _ := indexer.GetSCIDValuesByKey(variables, gnomon_scid, "signature", indexer.ChainHeight)

				// Check  if keysstring is nil or not to avoid any sort of panics
				var sigstr string
				if len(keysstring) > 0 {
					sigstr = keysstring[0]
				}

				validated, _, err := indexer.ValidateSCSignature(code, sigstr)
				if err != nil {
					log.Printf("%v", err)
				}

				// Ensure SC signature is validated (LOAD("signature") checks out to code validation)
				if validated || err != nil {
					log.Printf("[StartDaemonMode-fastsync] Gnomon SC '%v' code VALID - proceeding to inject scid data.", gnomon_scid)

					scidstoadd := make(map[string]*structures.FastSyncImport)

					// Check k/v pairs for the necessary info: keys/values - scid/headers, scidowner/owner, scidheight/height
					for _, v := range variables {
						switch ckey := v.Key.(type) {
						case string:
							if v.Value != nil {
								switch len(ckey) {
								case 64:
									// Check for k/v scid/headers
									if scidstoadd[ckey] == nil {
										scidstoadd[ckey] = &structures.FastSyncImport{}
									}
									scidstoadd[ckey].Headers = v.Value.(string)
								case 69:
									// Check for k/v scidowner/owner
									if scidstoadd[ckey[0:64]] == nil {
										scidstoadd[ckey[0:64]] = &structures.FastSyncImport{}
									}
									scidstoadd[ckey[0:64]].Owner = v.Value.(string)
								case 70:
									// Check for k/v scidheight/height
									if scidstoadd[ckey[0:64]] == nil {
										scidstoadd[ckey[0:64]] = &structures.FastSyncImport{}
									}
									scidstoadd[ckey[0:64]].Height = v.Value.(uint64)
								default:
									// Nothing - only should match defined ckey lengths
								}
							}
						default:
							// Nothing - expect only string for value types specifically to Gnomon
						}
					}

					err := indexer.AddSCIDToIndex(scidstoadd)
					if err != nil {
						log.Printf("[StartDaemonMode-fastsync] ERR - adding scids to index - %v", err)
					}
				} else {
					log.Printf("[StartDaemonMode-fastsync] Gnomon SC '%v' code was NOT validated against in-built signature variable. Skipping auto-population of scids.", gnomon_scid)
				}
			}
		}
	}

	if blockParallelNum <= 0 {
		blockParallelNum = 1
	}
	log.Printf("[StartDaemonMode] Set number of parallel blocks to index to '%v'", blockParallelNum)

	go func() {
		k := 0
		for {
			if indexer.Closing {
				// Break out on closing call
				break
			}

			// Temp stop for testing/checking data
			/*
				if indexer.LastIndexedHeight >= 1000 {
					log.Printf("Indexer reached %v , sleeping 60 seconds.", indexer.LastIndexedHeight)
					time.Sleep(time.Second * 60)
					continue
				}
			*/

			if indexer.LastIndexedHeight >= indexer.ChainHeight {
				time.Sleep(1 * time.Second)
				continue
			}

			// Check to cover fastsync scenarios; no reason to pull all this logic into the multi-block scanning components - this will only run once
			if k == 0 {
				_, err := indexer.RPC.getBlockHash(uint64(indexer.LastIndexedHeight))
				if err != nil {
					// Handle pruned nodes index errors... find height that they have blocks able to be indexed
					//log.Printf("Checking if strings contain: %v", err.Error())
					if strings.Contains(err.Error(), "err occured empty block") || strings.Contains(err.Error(), "err occured file does not exist") {
						currIndex := indexer.LastIndexedHeight
						rewindIndex := int64(0)
						for {
							if indexer.Closing {
								// If we do concurrent blocks in the future, this will need to move/be modified to be *after* all concurrent blocks are done incase exit etc.
								writeWait, _ := time.ParseDuration("50ms")
								for indexer.Backend.Writing == 1 {
									if indexer.Closing {
										return
									}
									//log.Printf("[StartDaemonMode-indexBlockgofunc] GravitonDB is writing... sleeping for %v...", writeWait)
									time.Sleep(writeWait)
								}
								indexer.Backend.Writing = 1
								indexer.Backend.StoreLastIndexHeight(currIndex, false)
								indexer.Backend.Writing = 0
								// Break out on closing call
								break
							}
							_, err = indexer.RPC.getBlockHash(uint64(currIndex))
							if err != nil {
								//if strings.Contains(err.Error(), "err occured empty block") {
								//time.Sleep(200 * time.Millisecond)	// sleep for node spam, not *required* but can be useful for lesser nodes in brief catchup time.
								// Increase block by 10 to not spam the daemon at every single block, but skip along a little bit to move faster/more less impact to node. This can be modified if required.
								if (currIndex + block_jump) > indexer.ChainHeight {
									currIndex = indexer.ChainHeight
								} else {
									currIndex += block_jump
								}
								log.Printf("GetBlock failed - checking %v", currIndex)
								//}
							} else {
								// Self-contain and loop through at most 10 or X blocks
								log.Printf("GetBlock worked at %v", currIndex)
								for {
									if indexer.Closing {
										// If we do concurrent blocks in the future, this will need to move/be modified to be *after* all concurrent blocks are done incase exit etc.
										writeWait, _ := time.ParseDuration("50ms")
										for indexer.Backend.Writing == 1 {
											if indexer.Closing {
												return
											}
											//log.Printf("[StartDaemonMode-indexBlockgofunc] GravitonDB is writing... sleeping for %v...", writeWait)
											time.Sleep(writeWait)
										}
										indexer.Backend.Writing = 1
										indexer.Backend.StoreLastIndexHeight(rewindIndex, false)
										indexer.Backend.Writing = 0

										// Break out on closing call
										break
									}
									if rewindIndex == 0 {
										rewindIndex = currIndex - block_jump + 1
										if rewindIndex < 0 {
											rewindIndex = 1
										}
									} else {
										log.Printf("Checking GetBlock at %v", rewindIndex)
										_, err = indexer.RPC.getBlockHash(uint64(rewindIndex))
										if err != nil {
											rewindIndex++
											//time.Sleep(200 * time.Millisecond)	// sleep for node spam, not *required* but can be useful for lesser nodes in brief catchup time.
										} else {
											log.Printf("GetBlock worked at %v - continuing as normal", rewindIndex+1)
											// Break out, we found the earliest block detail
											indexer.Lock()
											indexer.LastIndexedHeight = rewindIndex + 1
											indexer.Unlock()
											break
										}
									}
								}
								break
							}
						}
					}

					log.Printf("[mainFOR] ERROR - %v\n", err)
					time.Sleep(1 * time.Second)
					continue
				}
				k++
			}

			if indexer.LastIndexedHeight+int64(blockParallelNum) > indexer.ChainHeight {
				blockParallelNum = int(indexer.ChainHeight - indexer.LastIndexedHeight)
				//log.Printf("Parallel height is greater , setting blockParallelNum to %v : %v - %v", blockParallelNum, indexer.ChainHeight, indexer.LastIndexedHeight)

				if blockParallelNum <= 0 || indexer.LastIndexedHeight == indexer.ChainHeight {
					//log.Printf("blockParallelNum (%v) is <= 0 or lastindex (%v) and chain height (%v) are equal", blockParallelNum, indexer.LastIndexedHeight, indexer.ChainHeight)
					time.Sleep(1 * time.Second)
					continue
				}
			}

			var regTxCount int64
			var burnTxCount int64
			var normTxCount int64
			var wg sync.WaitGroup
			wg.Add(blockParallelNum)

			var blsctxnsLock sync.RWMutex
			var blIndexTxns []*structures.BlockTxns

			for i := 1; i <= blockParallelNum; i++ {
				go func(i int) {
					if indexer.Closing {
						wg.Done()
						return
					}
					currBlHeight := indexer.LastIndexedHeight + int64(i)

					blid, err := indexer.RPC.getBlockHash(uint64(currBlHeight))
					if err != nil {
						log.Printf("[StartDaemonMode-mainFOR-getBlockHash] %v - ERROR - getBlockHash(%v) - %v", currBlHeight, uint64(currBlHeight), err)
						wg.Done()
						return
					}

					blockTxns, err := indexer.indexBlock(blid, currBlHeight)
					if err != nil {
						log.Printf("[StartDaemonMode-mainFOR-indexBlock] %v - ERROR - indexBlock(%v) - %v", currBlHeight, blid, err)
						wg.Done()
						return
					}

					if len(blockTxns.Tx_hashes) > 0 {
						blsctxnsLock.Lock()
						blIndexTxns = append(blIndexTxns, blockTxns)
						blsctxnsLock.Unlock()
						wg.Done()
					} else {
						wg.Done()
					}
				}(i)
			}
			wg.Wait()

			if indexer.Closing {
				break
			}

			// Arrange blIndexTxns by height so processed linearly
			sort.SliceStable(blIndexTxns, func(i, j int) bool {
				return blIndexTxns[i].Topoheight < blIndexTxns[j].Topoheight
			})

			// Run through blocks one at a time here to max cpu on a given block if large txns rather than split cpu across go routines of multiple blocks
			for _, v := range blIndexTxns {
				if len(v.Tx_hashes) > 0 {
					c_sctxs, cregTxCount, cburnTxCount, cnormTxCount, err := indexer.IndexTxn(v)
					if err != nil {
						log.Printf("[StartDaemonMode-mainFOR-IndexTxn] %v - ERROR - IndexTxn(%v) - %v", v.Topoheight, v.Tx_hashes, err)
						return
					}

					regTxCount += cregTxCount
					burnTxCount += cburnTxCount
					normTxCount += cnormTxCount

					err = indexer.indexInvokes(c_sctxs, v)
					if err != nil {
						log.Printf("[StartDaemonMode-mainFOR-indexInvokes]  ERROR - %v", err)
						break
					}
				}
			}
			if err != nil {
				log.Printf("[StartDaemonMode-mainFOR-TxnIndexErrs] ERROR - %v", err)
				continue
			}

			if (regTxCount > 0 || burnTxCount > 0 || normTxCount > 0) && !(indexer.RunMode == "asset") {
				err = indexer.indexTxCounts(regTxCount, burnTxCount, normTxCount)
				if err != nil {
					log.Printf("[StartDaemonMode-mainFOR-indexTxCounts] ERROR - %v", err)
					continue
				}
			}

			if indexer.LastIndexedHeight <= indexer.LastIndexedHeight+int64(blockParallelNum) {
				indexer.Lock()
				indexer.LastIndexedHeight += int64(blockParallelNum)
				indexer.Unlock()

				writeWait, _ := time.ParseDuration("20ms")
				for indexer.Backend.Writing == 1 {
					if indexer.Closing {
						return
					}
					//log.Printf("[StartDaemonMode-indexBlockgofunc] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}
				indexer.Backend.Writing = 1
				_, _, err := indexer.Backend.StoreLastIndexHeight(indexer.LastIndexedHeight, false)
				if err != nil {
					log.Printf("[StartDaemonMode-mainFOR-StoreLastIndexHeight] ERROR - %v", err)
				}
				indexer.Backend.Writing = 0
			}
		}
	}()
}

// Potential future item - may be removed as primary srevice of Gnomon is against daemon and not wallet due to security
func (indexer *Indexer) StartWalletMode(runType string) {
	var err error

	// Simple connect loop .. if connection fails initially then keep trying, else break out and continue on. Connect() is handled in getInfo() for retries later on if connection ceases again
	/*
		TODO:
		var astr []string
		astr = append(astr, "Basic dGVzdDp0ZXN0cGFzcw==")
		client.WS, _, err = websocket.DefaultDialer.Dial("ws://"+endpoint+"/ws", http.Header{"Authorization": astr})
	*/
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		log.Printf("[StartDaemonMode] Trying to connect...")
		err = indexer.RPC.Connect(indexer.Endpoint)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}
		break
	}
	time.Sleep(1 * time.Second)

	// Continuously getInfo from daemon to update topoheight globally
	switch runType {
	case "receive":
		// do receive actions here (e.g. from data source via API/WS)
		// TODO: is there anything we need to do within indexer itself if just receiving?
	default:
		// 'retrieve'/etc.
		go indexer.getWalletHeight()
		time.Sleep(1 * time.Second)

		go func() {
			for {
				if indexer.Closing {
					// Break out on closing call
					break
				}

				if indexer.LastIndexedHeight > indexer.ChainHeight {
					time.Sleep(1 * time.Second)
					continue
				}

				// Do indexing calls here

				// TODO: Modify this to be the height of the *next* tx index etc.
				indexer.Lock()
				indexer.LastIndexedHeight++
				indexer.Unlock()
			}
		}()
	}

	// Hold
	select {}
}

// Manually add/inject a SCID to be indexed. Checks validity and then stores within owner tree (no signer addr) and stores a set of current variables.
func (indexer *Indexer) AddSCIDToIndex(scidstoadd map[string]*structures.FastSyncImport) (err error) {
	var wg sync.WaitGroup
	wg.Add(len(scidstoadd))

	var scilock sync.RWMutex
	var scidstoindexstage []SCIDToIndexStage

	log.Printf("[AddSCIDToIndex] Starting - Sorting %v SCIDs to index", len(scidstoadd))
	tempdb, err := storage.NewGravDBRAM("25ms")
	if err != nil {
		return fmt.Errorf("[AddSCIDToIndex] Error creating new gravdb: %v", err)
	}
	var treenames []string
	// We know owner is a tree that'll be written to, no need to loop through the scexists func every time when we *know* this one exists and isn't unique by scid etc.
	treenames = append(treenames, "owner")
	for scid, fsi := range scidstoadd {
		go func(scid string, fsi *structures.FastSyncImport) {
			// Check if already validated
			if scidExist(indexer.ValidatedSCs, scid) || indexer.Closing {
				//log.Printf("[AddSCIDToIndex] SCID '%v' already in validated list.", scid)
				wg.Done()

				return
			} else {
				// Validate SCID is *actually* a valid SCID
				scVars, scCode, _ := indexer.RPC.GetSCVariables(scid, indexer.ChainHeight)

				var contains bool

				// If we can get the SC and searchfilter is "" (get all), contains is true. Otherwise evaluate code against searchfilter
				if len(indexer.SearchFilter) == 0 {
					contains = true
				} else {
					// Ensure scCode is not blank (e.g. an invalid scid)
					if scCode != "" {
						for _, sfv := range indexer.SearchFilter {
							contains = strings.Contains(scCode, sfv)
							if contains {
								// Break b/c we want to ensure contains remains true. Only care if it matches at least 1 case
								break
							}
						}
					}
				}

				scilock.Lock()
				scidstoindexstage = append(scidstoindexstage, SCIDToIndexStage{scid: scid, fsi: fsi, scVars: scVars, scCode: scCode, contains: contains})
				scilock.Unlock()
			}
			wg.Done()
		}(scid, fsi)
	}
	wg.Wait()

	for _, v := range scidstoindexstage {
		if v.contains {
			// By returning valid variables of a given Scid (GetSC --> parse vars), we can conclude it is a valid SCID. Otherwise, skip adding to validated scids
			if len(v.scVars) > 0 {
				indexer.Lock()
				indexer.ValidatedSCs = append(indexer.ValidatedSCs, v.scid)
				indexer.Unlock()
				if v.fsi != nil {
					//log.Printf("[AddSCIDToIndex] SCID matches search filter. Adding SCID %v / Signer %v", scid, fsi.Owner)
				} else {
					//log.Printf("[AddSCIDToIndex] SCID matches search filter. Adding SCID %v", scid)
				}
				writeWait, _ := time.ParseDuration("10ms")
				for tempdb.Writing == 1 {
					if indexer.Closing {
						wg.Done()
						return
					}
					//log.Printf("[AddSCIDToIndex] GravitonDB is writing... sleeping for %v...", writeWait)
					time.Sleep(writeWait)
				}
				if indexer.Closing {
					wg.Done()
					return
				}
				tempdb.Writing = 1
				var ctrees []*graviton.Tree

				var sochanges bool
				var sotree *graviton.Tree
				if v.fsi != nil {
					sotree, sochanges, err = tempdb.StoreOwner(v.scid, v.fsi.Owner, true)
				} else {
					sotree, sochanges, err = tempdb.StoreOwner(v.scid, "", true)
				}
				if err != nil {
					log.Printf("[AddSCIDToIndex] ERR - storing owner: %v\n", err)
				} else {
					if sochanges {
						ctrees = append(ctrees, sotree)
					}
				}
				svdtree, svdchanges, err := tempdb.StoreSCIDVariableDetails(v.scid, v.scVars, indexer.ChainHeight, true)
				if err != nil {
					log.Printf("[AddSCIDToIndex] ERR - storing scid variable details: %v\n", err)
				} else {
					if svdchanges {
						ctrees = append(ctrees, svdtree)
					}
				}
				if !scidExist(treenames, v.scid+"vars") {
					treenames = append(treenames, v.scid+"vars")
				}
				sihtree, sihchanges, err := tempdb.StoreSCIDInteractionHeight(v.scid, indexer.ChainHeight, true)
				if err != nil {
					log.Printf("[AddSCIDToIndex] ERR - storing scid interaction height: %v\n", err)
				} else {
					if sihchanges {
						ctrees = append(ctrees, sihtree)
					}
				}
				if !scidExist(treenames, v.scid+"heights") {
					treenames = append(treenames, v.scid+"heights")
				}
				if len(ctrees) > 0 {
					_, err := tempdb.CommitTrees(ctrees)
					if err != nil {
						log.Printf("[AddSCIDToIndex] ERR - committing trees: %v\n", err)
					} else {
						//log.Printf("[AddSCIDToIndex] DEBUG - cv [%v]\n", cv)
					}
				}
				tempdb.Writing = 0
			} else {
				log.Printf("[AddSCIDToIndex] ERR - SCID '%v' doesn't exist at height %v", v.scid, indexer.ChainHeight)
			}
		}
	}

	log.Printf("[AddSCIDToIndex] Done - Sorting %v SCIDs to index", len(scidstoadd))
	// TODO: Sometimes the RAM store does not properly take in all values and are missing some index SCs. To investigate...
	log.Printf("[AddSCIDToIndex] Current stored disk: %v", len(indexer.Backend.GetAllOwnersAndSCIDs()))
	log.Printf("[AddSCIDToIndex] Current stored ram: %v", len(tempdb.GetAllOwnersAndSCIDs()))

	log.Printf("[AddSCIDToIndex] Starting - Committing RAM SCID sort to disk storage...")
	writeWait, _ := time.ParseDuration("10ms")
	for tempdb.Writing == 1 || indexer.Backend.Writing == 1 {
		if indexer.Closing {
			return
		}
		log.Printf("[AddSCIDToIndex-StoreAltDBInput] GravitonDB is writing... sleeping for %v...", writeWait)
		time.Sleep(writeWait)
	}
	tempdb.Writing = 1
	indexer.Backend.Writing = 1
	indexer.Backend.StoreAltDBInput(treenames, tempdb)
	tempdb.Writing = 0
	indexer.Backend.Writing = 0
	log.Printf("[AddSCIDToIndex] Done - Committing RAM SCID sort to disk storage...")
	log.Printf("[AddSCIDToIndex] New stored disk: %v", len(indexer.Backend.GetAllOwnersAndSCIDs()))

	return err
}

func (client *Client) Connect(endpoint string) (err error) {
	// Used to check if the endpoint has changed.. if so, then close WS to current and update WS
	if client.WS != nil {
		remAddr := client.WS.RemoteAddr()
		var pingpong string
		err2 := client.RPC.CallResult(context.Background(), "DERO.Ping", nil, &pingpong)
		if strings.Contains(remAddr.String(), endpoint) && err2 == nil {
			// Endpoint is the same, continue on
			return
		} else {
			// Remote addr (current ws connection endpoint) does not match indexer endpoint - re-connecting
			client.Lock()
			defer client.Unlock()
			client.WS.Close()
		}
	}

	client.WS, _, err = websocket.DefaultDialer.Dial("ws://"+endpoint+"/ws", nil)

	// notify user of any state change
	// if daemon connection breaks or comes live again
	if err == nil {
		if !Connected {
			log.Printf("[Connect] Connection to RPC server successful - ws://%s/ws\n", endpoint)
			Connected = true
		}
	} else {
		log.Printf("[Connect] ERROR connecting to wallet %v\n", err)

		if Connected {
			log.Printf("[Connect] ERROR - Connection to RPC server Failed - ws://%s/ws\n", endpoint)
		}
		Connected = false
		return err
	}

	input_output := rwc.New(client.WS)
	client.RPC = jrpc2.NewClient(channel.RawJSON(input_output, input_output), nil)

	return err
}

func (indexer *Indexer) indexBlock(blid string, topoheight int64) (blockTxns *structures.BlockTxns, err error) {
	blockTxns = &structures.BlockTxns{}

	var io rpc.GetBlock_Result
	var ip = rpc.GetBlock_Params{Hash: blid}

	if indexer.Closing {
		return
	}

	if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetBlock", ip, &io); err != nil {
		return blockTxns, fmt.Errorf("[indexBlock] ERROR - GetBlock failed: %v", err)
	}

	var bl block.Block
	var block_bin []byte

	block_bin, _ = hex.DecodeString(io.Blob)
	bl.Deserialize(block_bin)

	if indexer.MBLLookup {
		mbldetails, err2 := mbllookup.GetMBLByBLHash(bl)
		if err2 != nil {
			log.Printf("[indexBlock] Error getting miniblock details for blid %v", bl.GetHash().String())
			return blockTxns, err2
		}

		writeWait, _ := time.ParseDuration("50ms")
		for indexer.Backend.Writing == 1 {
			if indexer.Closing {
				return
			}
			//log.Printf("[Indexer-indexBlock-storeminiblockdetails] GravitonDB is writing... sleeping for %v...", writeWait)
			time.Sleep(writeWait)
		}
		if !(indexer.RunMode == "asset") {
			indexer.Backend.Writing = 1
			_, _, err2 = indexer.Backend.StoreMiniblockDetailsByHash(blid, mbldetails, false)
			if err2 != nil {
				log.Printf("[indexBlock] Error storing miniblock details for blid %v", err2)
				return blockTxns, err2
			}
			indexer.Backend.Writing = 0
		}
	}

	blockTxns.Topoheight = int64(bl.Height)
	blockTxns.Tx_hashes = bl.Tx_hashes

	return
}

func (indexer *Indexer) IndexTxn(blTxns *structures.BlockTxns) (bl_sctxs []structures.SCTXParse, regTxCount int64, burnTxCount int64, normTxCount int64, err error) {
	var txslock sync.RWMutex

	var wg sync.WaitGroup
	wg.Add(len(blTxns.Tx_hashes))

	for i := 0; i < len(blTxns.Tx_hashes); i++ {
		go func(i int) {
			if indexer.Closing {
				wg.Done()
				return
			}

			// We can match the PoW scheme result to filter out reg txns without needing to waste GetTransaction calls against saving time - https://github.com/deroproject/derohe/blob/main/cmd/dero-wallet-cli/easymenu_post_open.go#L150
			if blTxns.Tx_hashes[i][0] == 0 && blTxns.Tx_hashes[i][1] == 0 && blTxns.Tx_hashes[i][2] == 0 {
				txslock.Lock()
				regTxCount++
				txslock.Unlock()
				wg.Done()
				return
			}

			var tx transaction.Transaction
			var sc_args rpc.Arguments
			var sc_fees uint64
			var sender string

			var inputparam rpc.GetTransaction_Params
			var output rpc.GetTransaction_Result

			inputparam.Tx_Hashes = append(inputparam.Tx_Hashes, blTxns.Tx_hashes[i].String())

			if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetTransaction", inputparam, &output); err != nil {
				log.Printf("[IndexTxn] ERROR - GetTransaction for txid '%v' failed: %v\n", inputparam.Tx_Hashes, err)
				//return err
				//continue
				// TODO - In event indexer.Endpoint is being swapped, this case will fail and you could miss a txn. Need another handle rather than just "assume" skip/move on.
				wg.Done()
				// If we error, this could be due to regtxn not valid on pruned node or other reasons. We will just nil the err and then return and move on.
				err = nil
				return
			}

			tx_bin, _ := hex.DecodeString(output.Txs_as_hex[0])
			tx.Deserialize(tx_bin)

			// TODO: Add count for registration TXs and store the following on normal txs: IF SCID IS PRESENT, store tx details + ring members + fees + etc. Use later for scid balance queries
			if tx.TransactionType == transaction.SC_TX {
				sc_args = tx.SCDATA
				sc_fees = tx.Fees()
				var method string
				var scid string
				var scid_hex []byte

				entrypoint := fmt.Sprintf("%v", sc_args.Value("entrypoint", "S"))

				sc_action := fmt.Sprintf("%v", sc_args.Value("SC_ACTION", "U"))

				// Other ways to parse this, but will do for now --> see https://github.com/deroproject/derohe/blob/main/blockchain/blockchain.go#L688
				if sc_action == "1" {
					method = "installsc"
					scid = string(blTxns.Tx_hashes[i].String())
					scid_hex = []byte(scid)
				} else {
					method = "scinvoke"
					// Get "SC_ID" which is of type H to byte.. then to string
					scid_hex = []byte(fmt.Sprintf("%v", sc_args.Value("SC_ID", "H")))
					scid = string(scid_hex)
				}

				// TODO: What if there are multiple payloads with potentially different ringsizes, can that happen?
				if tx.Payloads[0].Statement.RingSize == 2 {
					sender = output.Txs[0].Signer
				} else {
					//log.Printf("[indexBlock] ERR - Ringsize for %v is != 2. Storing blank value for txid sender.\n", bl.Tx_hashes[i])
					//continue
					/*
						if method == "installsc" {
							// We do not store a ringsize > 2 of installsc calls. Only of SC interactions via sc_invoke for ringsize > 2 and just blank out the sender
							//continue
							//wg.Done()
						}
					*/
				}
				//time.Sleep(2 * time.Second)
				txslock.Lock()
				bl_sctxs = append(bl_sctxs, structures.SCTXParse{Txid: blTxns.Tx_hashes[i].String(), Scid: scid, Scid_hex: scid_hex, Entrypoint: entrypoint, Method: method, Sc_args: sc_args, Sender: sender, Payloads: tx.Payloads, Fees: sc_fees, Height: blTxns.Topoheight})
				txslock.Unlock()
			} else if tx.TransactionType == transaction.REGISTRATION {
				txslock.Lock()
				regTxCount++
				txslock.Unlock()
			} else if tx.TransactionType == transaction.BURN_TX {
				// TODO: Handle burn_tx here
				txslock.Lock()
				burnTxCount++
				txslock.Unlock()
			} else if tx.TransactionType == transaction.NORMAL {
				// TODO: Handle normal tx here
				txslock.Lock()
				normTxCount++
				txslock.Unlock()

				for j := 0; j < len(tx.Payloads); j++ {
					var zhash crypto.Hash
					if tx.Payloads[j].SCID != zhash {
						//log.Printf("[indexBlock] TXID '%v' has SCID in payload of '%v' and ring members: %v.", bl.Tx_hashes[i], tx.Payloads[j].SCID, output.Txs[j].Ring[j])
						for _, v := range output.Txs[0].Ring[j] {
							//bl_normtxs = append(bl_normtxs, structures.NormalTXWithSCIDParse{Txid: bl.Tx_hashes[i].String(), Scid: tx.Payloads[j].SCID.String(), Fees: tx_fees, Height: int64(bl.Height)})
							writeWait, _ := time.ParseDuration("50ms")
							for indexer.Backend.Writing == 1 {
								if indexer.Closing {
									return
								}
								//log.Printf("[Indexer-indexBlock-normTx-txLoop] GravitonDB is writing... sleeping for %v...", writeWait)
								time.Sleep(writeWait)
							}
							if indexer.Closing {
								return
							}
							if !(indexer.RunMode == "asset") {
								indexer.Backend.Writing = 1
								indexer.Backend.StoreNormalTxWithSCIDByAddr(v, &structures.NormalTXWithSCIDParse{Txid: blTxns.Tx_hashes[i].String(), Scid: tx.Payloads[j].SCID.String(), Fees: sc_fees, Height: int64(blTxns.Topoheight)}, false)
								indexer.Backend.Writing = 0
							}
						}
					}
				}
			} else {
				//log.Printf("TX %v type is NOT handled.\n", bl.Tx_hashes[i])
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	return bl_sctxs, regTxCount, burnTxCount, normTxCount, err
}

func (indexer *Indexer) indexTxCounts(regTxCount int64, burnTxCount int64, normTxCount int64) (err error) {
	if indexer.Closing {
		return
	}
	var ctrees []*graviton.Tree
	writeWait, _ := time.ParseDuration("50ms")
	for indexer.Backend.Writing == 1 {
		if indexer.Closing {
			return
		}
		//log.Printf("[Indexer-indexBlock-regTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
		time.Sleep(writeWait)
	}
	indexer.Backend.Writing = 1

	if regTxCount > 0 && !indexer.Fastsync {
		// Load from mem existing regTxCount and append new value
		currRegTxCount := indexer.Backend.GetTxCount("registration")
		/*
			writeWait, _ := time.ParseDuration("50ms")
			for indexer.Backend.Writing == 1 {
				if indexer.Closing {
					return
				}
				//log.Printf("[Indexer-indexBlock-regTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
				time.Sleep(writeWait)
			}
			indexer.Backend.Writing = 1
		*/
		rtxtree, rtxchanges, err := indexer.Backend.StoreTxCount(regTxCount+currRegTxCount, "registration", true)
		//indexer.Backend.Writing = 0
		if err != nil {
			log.Printf("[indexBlock] ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currRegTxCount, regTxCount, regTxCount+currRegTxCount)
			indexer.Backend.Writing = 0
			return err
		} else {
			if rtxchanges {
				ctrees = append(ctrees, rtxtree)
			}
		}
	}

	if burnTxCount > 0 && !indexer.Fastsync {
		// Load from mem existing burnTxCount and append new value
		currBurnTxCount := indexer.Backend.GetTxCount("burn")
		/*
			writeWait, _ := time.ParseDuration("50ms")
			for indexer.Backend.Writing == 1 {
				if indexer.Closing {
					return
				}
				//log.Printf("[Indexer-indexBlock-burnTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
				time.Sleep(writeWait)
			}
			indexer.Backend.Writing = 1
		*/
		btxtree, btxchanges, err := indexer.Backend.StoreTxCount(burnTxCount+currBurnTxCount, "burn", true)
		if err != nil {
			log.Printf("[indexBlock] ERROR - Error storing burn tx count. DB '%v' - this block count '%v' - total '%v'", currBurnTxCount, burnTxCount, regTxCount+currBurnTxCount)
			indexer.Backend.Writing = 0
			return err
		} else {
			if btxchanges {
				ctrees = append(ctrees, btxtree)
			}
		}
		//indexer.Backend.Writing = 0
	}

	if normTxCount > 0 && !indexer.Fastsync {
		/*
			// Test code for finding highest tps block
			var io rpc.GetBlockHeaderByHeight_Result
			var ip = rpc.GetBlockHeaderByTopoHeight_Params{TopoHeight: bl.Height - 1}

			if err = client.RPC.CallResult(context.Background(), "DERO.GetBlockHeaderByTopoHeight", ip, &io); err != nil {
				log.Printf("[getBlockHash] GetBlockHeaderByTopoHeight failed: %v\n", err)
				return err
			} else {
				//log.Printf("[getBlockHash] Retrieved block header from topoheight %v\n", height)
				//mainnet = !info.Testnet // inverse of testnet is mainnet
				//log.Printf("%v\n", io)
			}

			blid := io.Block_Header.Hash

			var io2 rpc.GetBlock_Result
			var ip2 = rpc.GetBlock_Params{Hash: blid}

			if err = client.RPC.CallResult(context.Background(), "DERO.GetBlock", ip2, &io2); err != nil {
				log.Printf("[indexBlock] ERROR - GetBlock failed: %v\n", err)
				return err
			}

			var bl2 block.Block
			var block_bin2 []byte

			block_bin2, _ = hex.DecodeString(io2.Blob)
			bl2.Deserialize(block_bin2)

			prevtimestamp := bl2.Timestamp

			// Load from mem existing normTxCount and append new value
			currNormTxCount := Graviton_backend.GetTxCount("normal")

			//log.Printf("%v / (%v - %v)", normTxCount, int64(bl.Timestamp), int64(prevtimestamp))
			tps := normTxCount / ((int64(bl.Timestamp) - int64(prevtimestamp)) / 1000)

			//err := Graviton_backend.StoreTxCount(normTxCount+currNormTxCount, "normal")
			if tps > currNormTxCount {
				err := Graviton_backend.StoreTxCount(tps, "normal")
				if err != nil {
					log.Printf("ERROR - Error storing normal tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, tps, regTxCount+currNormTxCount)
				}

				err = Graviton_backend.StoreTxCount(blheight, "registration")
				if err != nil {
					log.Printf("ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, normTxCount, regTxCount+currNormTxCount)
				}

				err = Graviton_backend.StoreTxCount((int64(bl.Timestamp) - int64(prevtimestamp)), "burn")
				if err != nil {
					log.Printf("ERROR - Error storing registration tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, normTxCount, regTxCount+currNormTxCount)
				}
			}
		*/

		// Load from mem existing normTxCount and append new value
		currNormTxCount := indexer.Backend.GetTxCount("normal")
		/*
			writeWait, _ := time.ParseDuration("50ms")
			for indexer.Backend.Writing == 1 {
				if indexer.Closing {
					return
				}
				//log.Printf("[Indexer-indexBlock-normTxCount] GravitonDB is writing... sleeping for %v...", writeWait)
				time.Sleep(writeWait)
			}
			indexer.Backend.Writing = 1
		*/
		ntxtree, ntxchanges, err := indexer.Backend.StoreTxCount(normTxCount+currNormTxCount, "normal", true)
		if err != nil {
			log.Printf("[indexBlock] ERROR - Error storing normal tx count. DB '%v' - this block count '%v' - total '%v'", currNormTxCount, currNormTxCount, normTxCount+currNormTxCount)
			indexer.Backend.Writing = 0
			return err
		} else {
			if ntxchanges {
				ctrees = append(ctrees, ntxtree)
			}
		}
	}
	if len(ctrees) > 0 {
		_, err := indexer.Backend.CommitTrees(ctrees)
		if err != nil {
			log.Printf("[indexBlock-indexTxCounts] ERR - committing trees: %v\n", err)
		} else {
			//log.Printf("[indexBlock-installsc] DEBUG - cv [%v]\n", cv)
		}
	}
	indexer.Backend.Writing = 0

	return nil
}

func (indexer *Indexer) indexInvokes(bl_sctxs []structures.SCTXParse, bl_txns *structures.BlockTxns) (err error) {

	if indexer.Closing {
		return
	}

	if len(bl_sctxs) > 0 {
		//log.Printf("Block %v has %v SC tx(s).\n", bl.GetHash(), len(bl_sctxs))

		// TODO: Go routine possible for pre-storage components given the number of 'potential' getscvar calls that may be required.. could speed up indexing some more.
		for i := 0; i < len(bl_sctxs); i++ {
			if bl_sctxs[i].Method == "installsc" {
				var contains bool

				code := fmt.Sprintf("%v", bl_sctxs[i].Sc_args.Value("SC_CODE", "S"))

				// Temporary check - will need something more robust to code compare potentially all except InitializePrivate() with a given template file or other filter inputs.
				//contains := strings.Contains(code, "200 STORE(\"somevar\", 1)")
				if len(indexer.SearchFilter) == 0 {
					contains = true
				} else {
					for _, sfv := range indexer.SearchFilter {
						contains = strings.Contains(code, sfv)
						if contains {
							// Break b/c we want to ensure contains remains true. Only care if it matches at least 1 case
							break
						}
					}
				}

				if !contains {
					// Then reject the validation that this is an installsc action and move on
					log.Printf("[indexBlock-installsc] SCID %v does not contain the search filter string, moving on.\n", bl_sctxs[i].Scid)
				} else {
					// Gets the SC variables (key/value) at a given topoheight and then stores them
					scVars, _, _ := indexer.RPC.GetSCVariables(bl_sctxs[i].Scid, bl_txns.Topoheight)

					if len(scVars) > 0 {
						// Append into db for validated SC
						log.Printf("[indexBlock-installsc] SCID matches search filter. Adding SCID %v / Signer %v\n", bl_sctxs[i].Scid, bl_sctxs[i].Sender)
						indexer.Lock()
						indexer.ValidatedSCs = append(indexer.ValidatedSCs, bl_sctxs[i].Scid)
						indexer.Unlock()

						writeWait, _ := time.ParseDuration("50ms")
						for indexer.Backend.Writing == 1 {
							if indexer.Closing {
								return
							}
							//log.Printf("[Indexer-indexBlock-sctxshandle] GravitonDB is writing... sleeping for %v...", writeWait)
							time.Sleep(writeWait)
						}
						indexer.Backend.Writing = 1
						var ctrees []*graviton.Tree
						sotree, sochanges, err := indexer.Backend.StoreOwner(bl_sctxs[i].Scid, bl_sctxs[i].Sender, true)
						if err != nil {
							log.Printf("[indexBlock-installsc] Error storing owner: %v\n", err)
						} else {
							if sochanges {
								ctrees = append(ctrees, sotree)
							}
						}

						sidtree, sidchanges, err := indexer.Backend.StoreInvokeDetails(bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, bl_txns.Topoheight, &bl_sctxs[i], true)
						if err != nil {
							log.Printf("[indexBlock-installsc] Err storing invoke details. Err: %v\n", err)
							time.Sleep(5 * time.Second)
							return err
						} else {
							if sidchanges {
								ctrees = append(ctrees, sidtree)
							}
						}

						svdtree, svdchanges, err := indexer.Backend.StoreSCIDVariableDetails(bl_sctxs[i].Scid, scVars, bl_txns.Topoheight, true)
						if err != nil {
							log.Printf("[indexBlock-installsc] ERR - storing scid variable details: %v\n", err)
						} else {
							if svdchanges {
								ctrees = append(ctrees, svdtree)
							}
						}
						sihtree, sihchanges, err := indexer.Backend.StoreSCIDInteractionHeight(bl_sctxs[i].Scid, bl_txns.Topoheight, true)
						if err != nil {
							log.Printf("[indexBlock-installsc] ERR - storing scid interaction height: %v\n", err)
						} else {
							if sihchanges {
								ctrees = append(ctrees, sihtree)
							}
						}
						if len(ctrees) > 0 {
							_, err := indexer.Backend.CommitTrees(ctrees)
							if err != nil {
								log.Printf("[indexBlock-installsc] ERR - committing trees: %v\n", err)
							} else {
								//log.Printf("[indexBlock-installsc] DEBUG - cv [%v]\n", cv)
							}
						}
						indexer.Backend.Writing = 0

						//log.Printf("DEBUG -- SCID: %v ; Sender: %v ; Entrypoint: %v ; topoheight : %v ; info: %v", bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, topoheight, &bl_sctxs[i])
						//log.Printf("DEBUG -- Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v\n", bl_sctxs[i].Sender, bl_txns.Topoheight, bl_sctxs[i].Sc_args, bl_sctxs[i].Payloads[0].BurnValue)
					} else {
						log.Printf("[indexBlock-installsc] SCID '%v' appears to be invalid.", bl_sctxs[i].Scid)
						writeWait, _ := time.ParseDuration("50ms")
						for indexer.Backend.Writing == 1 {
							if indexer.Closing {
								return
							}
							//log.Printf("[Indexer-indexBlock-sctxshandle] GravitonDB is writing... sleeping for %v...", writeWait)
							time.Sleep(writeWait)
						}
						if !(indexer.RunMode == "asset") {
							indexer.Backend.Writing = 1
							indexer.Backend.StoreInvalidSCIDDeploys(bl_sctxs[i].Scid, bl_sctxs[i].Fees, false)
							indexer.Backend.Writing = 0
						}
					}
				}
			} else {
				if !scidExist(indexer.ValidatedSCs, bl_sctxs[i].Scid) {

					// Validate SCID is *actually* a valid SCID
					valVars, _, _ := indexer.RPC.GetSCVariables(bl_sctxs[i].Scid, bl_txns.Topoheight)

					// By returning valid variables of a given Scid (GetSC --> parse vars), we can conclude it is a valid SCID. Otherwise, skip adding to validated scids
					if len(valVars) > 0 {
						log.Printf("[indexBlock] SCID matches search filter. Adding SCID %v / Signer %v\n", bl_sctxs[i].Scid, "")
						indexer.Lock()
						indexer.ValidatedSCs = append(indexer.ValidatedSCs, bl_sctxs[i].Scid)
						indexer.Unlock()

						writeWait, _ := time.ParseDuration("50ms")
						for indexer.Backend.Writing == 1 {
							if indexer.Closing {
								return
							}
							//log.Printf("[Indexer-indexBlock-sctxshandle] GravitonDB is writing... sleeping for %v...", writeWait)
							time.Sleep(writeWait)
						}
						indexer.Backend.Writing = 1
						_, _, err = indexer.Backend.StoreOwner(bl_sctxs[i].Scid, "", false)
						if err != nil {
							log.Printf("[indexBlock] Error storing owner: %v\n", err)
						}
						indexer.Backend.Writing = 0
					}
				}

				if scidExist(indexer.ValidatedSCs, bl_sctxs[i].Scid) {
					//log.Printf("SCID %v is validated, checking the SC TX entrypoints to see if they should be logged.\n", bl_sctxs[i].Scid)
					// TODO: Modify this to be either all entrypoints, just Start, or a subset that is defined in pre-run params or not needed?
					//if bl_sctxs[i].entrypoint == "Start" {
					//if bl_sctxs[i].Entrypoint == "InputStr" {
					if true {
						currsctx := bl_sctxs[i]

						//log.Printf("Tx %v matches scinvoke call filter(s). Adding %v to DB.\n", bl_sctxs[i].Txid, currsctx)

						writeWait, _ := time.ParseDuration("50ms")
						for indexer.Backend.Writing == 1 {
							if indexer.Closing {
								return
							}
							//log.Printf("[Indexer-indexBlock-sctxshandle] GravitonDB is writing... sleeping for %v...", writeWait)
							time.Sleep(writeWait)
						}
						indexer.Backend.Writing = 1
						var ctrees []*graviton.Tree

						// If a hardcodedscid invoke + fastsync is enabled, do not log any new details. We will only retain within DB on-launch data.
						if scidExist(hardcodedscids, bl_sctxs[i].Scid) && indexer.Fastsync {
							log.Printf("[indexBlock] Skipping invoke detail store of '%v' since fastsync is '%v'.", bl_sctxs[i].Scid, indexer.Fastsync)
						} else {
							if !(indexer.RunMode == "asset") {
								sidtree, sidchanges, err := indexer.Backend.StoreInvokeDetails(bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, bl_txns.Topoheight, &currsctx, true)
								if err != nil {
									log.Printf("[indexBlock] Err storing invoke details. Err: %v\n", err)
									time.Sleep(5 * time.Second)
									return err
								} else {
									if sidchanges {
										ctrees = append(ctrees, sidtree)
									}
								}

								// Gets the SC variables (key/value) at a given topoheight and then stores them
								scVars, _, _ := indexer.RPC.GetSCVariables(bl_sctxs[i].Scid, bl_txns.Topoheight)
								svdtree, svdchanges, err := indexer.Backend.StoreSCIDVariableDetails(bl_sctxs[i].Scid, scVars, bl_txns.Topoheight, true)
								if err != nil {
									log.Printf("[indexBlock] ERR - storing scid variable details: %v\n", err)
								} else {
									if svdchanges {
										ctrees = append(ctrees, svdtree)
									}
								}
								sihtree, sihchanges, err := indexer.Backend.StoreSCIDInteractionHeight(bl_sctxs[i].Scid, bl_txns.Topoheight, true)
								if err != nil {
									log.Printf("[indexBlock] ERR - storing scid interaction height: %v\n", err)
								} else {
									if sihchanges {
										ctrees = append(ctrees, sihtree)
									}
								}
							}
						}

						if len(ctrees) > 0 {
							_, err := indexer.Backend.CommitTrees(ctrees)
							if err != nil {
								log.Printf("[indexBlock] ERR - committing trees: %v\n", err)
							} else {
								//log.Printf("[indexBlock] DEBUG - cv [%v]\n", cv)
							}
						}
						indexer.Backend.Writing = 0

						//log.Printf("DEBUG -- SCID: %v ; Sender: %v ; Entrypoint: %v ; topoheight : %v ; info: %v", bl_sctxs[i].Scid, bl_sctxs[i].Sender, bl_sctxs[i].Entrypoint, topoheight, &currsctx)
						//log.Printf("DEBUG -- Sender: %v ; topoheight : %v ; args: %v ; burnValue: %v\n", bl_sctxs[i].Sender, bl_txns.Topoheight, bl_sctxs[i].Sc_args, bl_sctxs[i].Payloads[0].BurnValue)
					} else {
						//log.Printf("Tx %v does not match scinvoke call filter(s), but %v instead. This should not (currently) be added to DB.\n", bl_sctxs[i].Txid, bl_sctxs[i].Entrypoint)
					}
				} else {
					//log.Printf("SCID %v is not validated and thus we do not log SC interactions for this. Moving on.\n", bl_sctxs[i].Scid)
				}
			}
		}
	} else {
		//log.Printf("Block %v does not have any SC txs\n", bl.GetHash())
	}

	return nil
}

// Check if value exists within a string array/slice
func scidExist(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

// DERO.GetBlockHeaderByTopoHeight rpc call for returning block hash at a particular topoheight
func (client *Client) getBlockHash(height uint64) (hash string, err error) {
	//log.Printf("[getBlockHash] Attempting to get block details at topoheight %v\n", height)

	var io rpc.GetBlockHeaderByHeight_Result
	var ip = rpc.GetBlockHeaderByTopoHeight_Params{TopoHeight: height}

	if err = client.RPC.CallResult(context.Background(), "DERO.GetBlockHeaderByTopoHeight", ip, &io); err != nil {
		//log.Printf("[getBlockHash] GetBlockHeaderByTopoHeight failed: %v\n", err)
		return hash, fmt.Errorf("GetBlockHeaderByTopoHeight failed: %v", err)
	} else {
		//log.Printf("[getBlockHash] Retrieved block header from topoheight %v\n", height)
		//mainnet = !info.Testnet // inverse of testnet is mainnet
		//log.Printf("%v\n", io)
	}

	hash = io.Block_Header.Hash

	return hash, err
}

// Looped interval to probe DERO.GetInfo rpc call for updating chain topoheight. Also handles keeping connection to daemon via RPC.Connect() calls
func (indexer *Indexer) getInfo() {
	var reconnect_count int
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		var err error

		// Check connection to be sure indexer.Endpoint hasn't changed. If it has, then update. Otherwise Connect will just return back no issues
		indexer.RPC.Connect(indexer.Endpoint)

		var info *structures.GetInfo

		// collect all the data afresh,  execute rpc to service
		if err = indexer.RPC.RPC.CallResult(context.Background(), "DERO.GetInfo", nil, &info); err != nil {
			//log.Printf("[getInfo] ERROR - GetInfo failed: %v\n", err)

			// TODO: Perhaps just a .Closing = true call here and then gnomonserver can be polling for any indexers with .Closing then close the rest cleanly. If packaged, then just have to handle themselves w/ .Close()
			if reconnect_count >= 5 && indexer.CloseOnDisconnect {
				indexer.Close()
				break
			}
			time.Sleep(1 * time.Second)
			indexer.RPC.Connect(indexer.Endpoint) // Attempt to re-connect now

			reconnect_count++

			continue
		} else {
			if reconnect_count > 0 {
				reconnect_count = 0
			}
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//log.Printf("%v\n", info)
		}

		currStoreGetInfo := indexer.Backend.GetGetInfoDetails()
		if currStoreGetInfo != nil {
			// Ensure you are not connecting to testnet or mainnet unintentionally based on store getinfo history
			if currStoreGetInfo.Testnet == info.Testnet {
				if currStoreGetInfo.Height < info.Height {
					structureGetInfo := info
					writeWait, _ := time.ParseDuration("50ms")
					for indexer.Backend.Writing == 1 {
						if indexer.Closing {
							return
						}
						//log.Printf("[Indexer-getInfo] GravitonDB is writing... sleeping for %v...", writeWait)
						time.Sleep(writeWait)
					}
					indexer.Backend.Writing = 1
					_, _, err := indexer.Backend.StoreGetInfoDetails(structureGetInfo, false)
					if err != nil {
						log.Printf("[getInfo] ERROR - GetInfo store failed: %v\n", err)
					}
					indexer.Backend.Writing = 0
				}
			} else {
				if indexer.RPC.WS != nil {
					// Remote addr (current ws connection endpoint) does not match indexer endpoint - re-connecting
					log.Printf("[getInfo] ERROR - Endpoint network (testnet - %v) is not the same as past stored network (testnet - %v)", info.Testnet, currStoreGetInfo.Testnet)
					indexer.RPC.Lock()
					indexer.RPC.WS.Close()
					indexer.RPC.Unlock()

					indexer.Lock()
					indexer.ChainHeight = 0
					indexer.Unlock()

					time.Sleep(5 * time.Second)
					continue
				}
			}
		} else {
			structureGetInfo := info
			writeWait, _ := time.ParseDuration("50ms")
			for indexer.Backend.Writing == 1 {
				if indexer.Closing {
					return
				}
				//log.Printf("[Indexer-getInfo] GravitonDB is writing... sleeping for %v...", writeWait)
				time.Sleep(writeWait)
			}
			indexer.Backend.Writing = 1
			_, _, err := indexer.Backend.StoreGetInfoDetails(structureGetInfo, false)
			if err != nil {
				log.Printf("[getInfo] ERROR - GetInfo store failed: %v\n", err)
			}
			indexer.Backend.Writing = 0
		}

		indexer.Lock()
		indexer.ChainHeight = info.TopoHeight
		indexer.Unlock()

		time.Sleep(5 * time.Second)
	}
}

// Looped interval to probe WALLET.GetHeight rpc call for updating wallet height
func (indexer *Indexer) getWalletHeight() {
	for {
		if indexer.Closing {
			// Break out on closing call
			break
		}
		var err error

		var info rpc.GetHeight_Result

		// collect all the data afresh,  execute rpc to service
		if err = indexer.RPC.RPC.CallResult(context.Background(), "WALLET.GetHeight", nil, &info); err != nil {
			log.Printf("[getWalletHeight] ERROR - GetHeight failed: %v\n", err)
			time.Sleep(1 * time.Second)
			indexer.RPC.Connect(indexer.Endpoint) // Attempt to re-connect now
			continue
		} else {
			//mainnet = !info.Testnet // inverse of testnet is mainnet
			//log.Printf("%v\n", info)
		}

		indexer.Lock()
		indexer.ChainHeight = int64(info.Height)
		indexer.Unlock()

		time.Sleep(5 * time.Second)
	}
}

// Gets SC variable details
func (client *Client) GetSCVariables(scid string, topoheight int64) (variables []*structures.SCIDVariable, code string, balances map[string]uint64) {
	var err error
	//balances = make(map[string]uint64)

	var getSCResults rpc.GetSC_Result
	getSCParams := rpc.GetSC_Params{SCID: scid, Code: true, Variables: true, TopoHeight: topoheight}
	if client.WS == nil {
		return
	}
	if err = client.RPC.CallResult(context.Background(), "DERO.GetSC", getSCParams, &getSCResults); err != nil {
		log.Printf("[GetSCVariables] ERROR - GetSCVariables failed: %v\n", err)
		return variables, code, balances
	}

	code = getSCResults.Code

	for k, v := range getSCResults.VariableStringKeys {
		currVar := &structures.SCIDVariable{}
		// TODO: Do we need to store "C" through these means? If we don't , need to update the len(scVars) etc. calls to ensure that even a 0 count goes through, but perhaps validate off code/balances
		/*
			if k == "C" {
				continue
			}
		*/
		currVar.Key = k
		switch cval := v.(type) {
		case float64:
			currVar.Value = uint64(cval)
		case uint64:
			currVar.Value = cval
		case string:
			// hex decode since all strings are hex encoded
			dstr, _ := hex.DecodeString(cval)
			p := new(crypto.Point)
			if err := p.DecodeCompressed(dstr); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				currVar.Value = string(dstr)
			}
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			currVar.Value = str
		}
		variables = append(variables, currVar)
	}

	for k, v := range getSCResults.VariableUint64Keys {
		currVar := &structures.SCIDVariable{}
		currVar.Key = k
		switch cval := v.(type) {
		case string:
			// hex decode since all strings are hex encoded
			decd, _ := hex.DecodeString(cval)
			p := new(crypto.Point)
			if err := p.DecodeCompressed(decd); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				currVar.Value = string(decd)
			}
		case uint64:
			currVar.Value = cval
		case float64:
			currVar.Value = uint64(cval)
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			currVar.Value = str
		}
		variables = append(variables, currVar)
	}

	balances = getSCResults.Balances

	return variables, code, balances
}

// Gets SC variable keys at given topoheight who's value equates to a given interface{} (string/uint64)
func (indexer *Indexer) GetSCIDKeysByValue(variables []*structures.SCIDVariable, scid string, val interface{}, height int64) (keysstring []string, keysuint64 []uint64) {
	// If variables were not provided, then fetch them.
	if len(variables) <= 0 {
		variables, _, _ = indexer.RPC.GetSCVariables(scid, height)
	}

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := val.(type) {
	case uint64:
		for _, v := range variables {
			switch cval := v.Value.(type) {
			case uint64:
				if inpvar == cval {
					switch ckey := v.Key.(type) {
					case float64:
						keysuint64 = append(keysuint64, uint64(ckey))
					case uint64:
						keysuint64 = append(keysuint64, ckey)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						keysstring = append(keysstring, v.Key.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	case string:
		for _, v := range variables {
			switch cval := v.Value.(type) {
			case string:
				if inpvar == cval {
					switch ckey := v.Key.(type) {
					case float64:
						keysuint64 = append(keysuint64, uint64(ckey))
					case uint64:
						keysuint64 = append(keysuint64, ckey)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						keysstring = append(keysstring, v.Key.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	default:
		// Nothing - expect only string/uint64 for value types
	}

	return keysstring, keysuint64
}

// Gets SC values by key at given topoheight who's key equates to a given interface{} (string/uint64)
func (indexer *Indexer) GetSCIDValuesByKey(variables []*structures.SCIDVariable, scid string, key interface{}, height int64) (valuesstring []string, valuesuint64 []uint64) {
	// If variables were not provided, then fetch them.
	if len(variables) <= 0 {
		variables, _, _ = indexer.RPC.GetSCVariables(scid, height)
	}

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := key.(type) {
	case uint64:
		for _, v := range variables {
			switch ckey := v.Key.(type) {
			case uint64:
				if inpvar == ckey {
					switch cval := v.Value.(type) {
					case float64:
						valuesuint64 = append(valuesuint64, uint64(cval))
					case uint64:
						valuesuint64 = append(valuesuint64, cval)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						valuesstring = append(valuesstring, v.Value.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	case string:
		for _, v := range variables {
			switch ckey := v.Key.(type) {
			case string:
				if inpvar == ckey {
					switch cval := v.Value.(type) {
					case float64:
						valuesuint64 = append(valuesuint64, uint64(cval))
					case uint64:
						valuesuint64 = append(valuesuint64, cval)
					default:
						// default just store as string. Values should only ever be strings or uint64, however, but assume default to string
						valuesstring = append(valuesstring, v.Value.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	default:
		// Nothing - expect only string/uint64 for value types
	}

	return valuesstring, valuesuint64
}

// Converts returned SCIDVariables KEY values who's values equates to a given interface{} (string/uint64)
func (indexer *Indexer) ConvertSCIDKeys(variables []*structures.SCIDVariable) (keysstring []string, keysuint64 []uint64) {
	for _, v := range variables {
		switch ckey := v.Key.(type) {
		case float64:
			keysuint64 = append(keysuint64, uint64(ckey))
		case uint64:
			keysuint64 = append(keysuint64, ckey)
		default:
			// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
			keysstring = append(keysstring, v.Key.(string))
		}
	}

	return keysstring, keysuint64
}

// Converts returned SCIDVariables VALUE values who's values equates to a given interface{} (string/uint64)
func (indexer *Indexer) ConvertSCIDValues(variables []*structures.SCIDVariable) (valuesstring []string, valuesuint64 []uint64) {
	for _, v := range variables {
		switch cval := v.Value.(type) {
		case float64:
			valuesuint64 = append(valuesuint64, uint64(cval))
		case uint64:
			valuesuint64 = append(valuesuint64, cval)
		default:
			// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
			valuesstring = append(valuesstring, v.Value.(string))
		}
	}

	return valuesstring, valuesuint64
}

// Validates that a stored signature results in the code deployed to a SC - currently allowing any 'key' to be passed through, however intended key is 'signature' or similar
func (indexer *Indexer) ValidateSCSignature(code string, key string) (validated bool, signer string, err error) {
	if key == "" {
		return
	}

	// CheckSignature
	filedata := []byte(key)
	p, _ := pem.Decode(filedata)
	if p == nil {
		log.Printf("[ValidateSCSignature] ERR - Unknown format of input data - %v", key)
		return
	}

	astr := p.Headers["Address"]
	cstr := p.Headers["C"]
	sstr := p.Headers["S"]

	addr, err := rpc.NewAddress(astr)
	if err != nil {
		log.Printf("[ValidateSCSignature] ERR - Cannot validate Address header")
		return
	}

	c, ok := new(big.Int).SetString(cstr, 16)
	if !ok {
		err = fmt.Errorf("[ValidateSCSignature] Unknown C format")
		return
	}

	s, ok := new(big.Int).SetString(sstr, 16)
	if !ok {
		err = fmt.Errorf("[ValidateSCSignature] Unknown S format")
		return
	}

	tmppoint := new(bn256.G1).Add(new(bn256.G1).ScalarMult(crypto.G, s), new(bn256.G1).ScalarMult(addr.PublicKey.G1(), new(big.Int).Neg(c)))
	serialize := []byte(fmt.Sprintf("%s%s%x", addr.PublicKey.G1().String(), tmppoint.String(), p.Bytes))

	c_calculated := crypto.ReducedHash(serialize)
	if c.String() != c_calculated.String() {
		err = fmt.Errorf("[ValidateSCSignature] signature mismatch")
		return
	}

	signer = addr.String()
	message := p.Bytes

	if string(message) == code {
		validated = true
	}

	return
}

// Close cleanly the indexer
func (ind *Indexer) Close() {
	// Tell indexer a closing operation is happening; this will close out loops on next iteration
	ind.Closing = true
	ind.Backend.Closing = true

	// Sleep for safety
	time.Sleep(time.Second * 1)

	// Close websocket connection cleanly
	if ind.RPC.WS != nil {
		ind.RPC.WS.Close()
	}

	// Close out grav db cleanly
	writeWait, _ := time.ParseDuration("10ms")
	for ind.Backend.Writing == 1 {
		time.Sleep(writeWait)
	}
	ind.Backend.Writing = 1
	ind.Backend.DB.Close()
	ind.Backend.Writing = 0
}
