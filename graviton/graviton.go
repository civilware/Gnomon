package graviton

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/deroproject/graviton"
)

type GravitonStore struct {
	DB            *graviton.Store
	DBFolder      string
	DBPath        string
	DBTrees       []string
	migrating     int
	DBMaxSnapshot uint64
	DBMigrateWait time.Duration
	Writing       int
}

type TreeKV struct {
	k []byte
	v []byte
}

type TXDetails struct {
	TimeStamp  int64
	Key        []byte
	ScValue    string
	Txid       string
	RawMessage string
	Sender     string
}

type SendDetails struct {
	TimeStamp int64
	Recipient string
	ScValue   string
	Key       []byte
}

type SentMessages struct {
	SentTXs []*SendDetails
}

type Messages struct {
	MessageTXs []*TXDetails
}

// ---- Graviton/Backend functions ---- //
// Mainnet TODO: Proper graviton/backend .go file(s)
// Builds new Graviton DB based on input from main()
func NewGravDB(dbtrees []string, dbFolder, dbmigratewait string, dbmaxsnapshot uint64) *GravitonStore {
	var Graviton_backend *GravitonStore = &GravitonStore{}

	current_path, err := os.Getwd()
	if err != nil {
		log.Printf("%v", err)
	}

	Graviton_backend.DBMigrateWait, _ = time.ParseDuration(dbmigratewait)

	Graviton_backend.DBMaxSnapshot = dbmaxsnapshot

	Graviton_backend.DBFolder = dbFolder

	Graviton_backend.DBPath = filepath.Join(current_path, dbFolder)

	Graviton_backend.DB, err = graviton.NewDiskStore(Graviton_backend.DBPath)
	if err != nil {
		log.Fatalf("[NewGravDB] Could not create db store: %v", err)
	}

	for i := 0; i < len(dbtrees); i++ {
		Graviton_backend.DBTrees = append(Graviton_backend.DBTrees, dbtrees[i])
	}

	return Graviton_backend
}

// Swaps the store pointer from existing to new after copying latest snapshot to new DB - fast as cursor + disk writes allow [possible other alternatives such as mem store for some of these interwoven, testing needed]
func (g *GravitonStore) SwapGravDB(dbFolder string) {
	// Use g.migrating as a simple 'mutex' of sorts to lock other read/write functions out of doing anything with DB until this function has completed.
	g.migrating = 1

	// Rename existing bak to bak2, then goroutine to cleanup so process doesn't wait for old db cleanup time
	var bakFolder string = dbFolder + "_bak"
	var bak2Folder string = dbFolder + "_bak2"
	log.Printf("[SwapGravDB] Renaming directory %v to %v", bakFolder, bak2Folder)
	os.Rename(bakFolder, bak2Folder)
	log.Printf("[SwapGravDB] Removing directory %v", bak2Folder)
	go os.RemoveAll(bak2Folder)

	// Get existing store values, defer close of original, and get store values for new DB to write to
	store := g.DB
	ss, _ := store.LoadSnapshot(0)

	tree, _ := ss.GetTree(g.DBTrees[0])
	log.Printf("[SwapGravDB] SS: %v", ss.GetVersion())

	c := tree.Cursor()
	log.Printf("[SwapGravDB] Getting k/v pairs")
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	var treeKV []*TreeKV // Just k & v which are of type []byte
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		temp := &TreeKV{k, v}
		treeKV = append(treeKV, temp)
	}
	log.Printf("[SwapGravDB] Closing store")
	store.Close()

	// Backup last set of g.DBMaxSnapshot snapshots, can offload elsewhere or make this process as X many times as you want to backup.
	var oldFolder string
	oldFolder = g.DBPath
	log.Printf("[SwapGravDB] Renaming directory %v to %v", oldFolder, bakFolder)
	os.Rename(oldFolder, bakFolder)

	log.Printf("[SwapGravDB] Creating new disk store")
	g.DB, _ = graviton.NewDiskStore(g.DBPath)

	// Take vals from previous DB store that were put into treeKV struct (array of), and commit to new DB after putting all k/v pairs back
	store = g.DB
	ss, _ = store.LoadSnapshot(0)
	tree, _ = ss.GetTree(g.DBTrees[0])

	log.Printf("[SwapGravDB] Putting k/v pairs into tree...")
	for _, val := range treeKV {
		tree.Put(val.k, val.v)
	}
	log.Printf("[SwapGravDB] Committing k/v pairs to tree")
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[SwapGravDB] ERROR: %v", cerr)
	}
	log.Printf("[SwapGravDB] Migration to new DB is done.")
	g.migrating = 0
}

// Gets TX details
func (g *GravitonStore) GetTXs() []*TXDetails {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[GetTXs] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}
	if ss.GetVersion() >= g.DBMaxSnapshot {
		g.SwapGravDB(g.DBFolder)

		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree(g.DBTrees[0]) // use or create tree named by poolhost in config
	key := "messages"
	var reply *Messages

	v, _ := tree.Get([]byte(key))
	if v != nil {
		_ = json.Unmarshal(v, &reply)
		return reply.MessageTXs
	}

	return nil
}

// Stores TX details
func (g *GravitonStore) StoreTX(txDetails *TXDetails) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreTX] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}
	if ss.GetVersion() >= g.DBMaxSnapshot {
		g.SwapGravDB(g.DBFolder)

		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree(g.DBTrees[0])
	key := "messages"

	currMessages, err := tree.Get([]byte(key))
	var messages *Messages

	var newMessages []byte

	if err != nil {
		// Returns key not found if != nil, or other err, but assuming keynotfound/leafnotfound
		var txDetailsArr []*TXDetails
		txDetailsArr = append(txDetailsArr, txDetails)
		messages = &Messages{MessageTXs: txDetailsArr}
	} else {
		// Retrieve value and convert to BlocksFoundByHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currMessages, &messages)

		messages.MessageTXs = append(messages.MessageTXs, txDetails)
	}
	newMessages, err = json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("[Graviton-StoreTX] could not marshal messages info: %v", err)
	}

	log.Printf("[Graviton-StoreTX] Storing %v", txDetails)
	tree.Put([]byte(key), []byte(newMessages)) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton-StoreTX] ERROR: %v", cerr)
	}
	return nil
}

// Gets Sent TX details
func (g *GravitonStore) GetSentTXs() []*SendDetails {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[GetSentTXs] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}
	if ss.GetVersion() >= g.DBMaxSnapshot {
		g.SwapGravDB(g.DBFolder)

		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree(g.DBTrees[0]) // use or create tree named by poolhost in config
	key := "sentmessages"
	var reply *SentMessages

	v, _ := tree.Get([]byte(key))
	if v != nil {
		_ = json.Unmarshal(v, &reply)
		return reply.SentTXs
	}

	return nil
}

// Stores Sent TX details
func (g *GravitonStore) StoreSentTX(txDetails *SendDetails) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreSentTX] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}
	if ss.GetVersion() >= g.DBMaxSnapshot {
		g.SwapGravDB(g.DBFolder)

		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree(g.DBTrees[0])
	key := "sentmessages"

	currMessages, err := tree.Get([]byte(key))
	var messages *SentMessages

	var newMessages []byte

	if err != nil {
		// Returns key not found if != nil, or other err, but assuming keynotfound/leafnotfound
		var txDetailsArr []*SendDetails
		txDetailsArr = append(txDetailsArr, txDetails)
		messages = &SentMessages{SentTXs: txDetailsArr}
	} else {
		// Retrieve value and convert to BlocksFoundByHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currMessages, &messages)

		messages.SentTXs = append(messages.SentTXs, txDetails)
	}
	newMessages, err = json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("[Graviton-StoreSentTX] could not marshal messages info: %v", err)
	}

	log.Printf("[Graviton-StoreSentTX] Storing %v", txDetails)
	tree.Put([]byte(key), []byte(newMessages)) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton-StoreSentTX] ERROR: %v", cerr)
	}
	return nil
}

// ---- End Graviton/Backend functions ---- //
