package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/civilware/Gnomon/structures"
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
	Closing       bool
}

// TODO/NOTE: Lots of optimization/modifications are to be had here. Commit handling, tree structures, folder structure etc to reduce disk usage. It's higher now than will be in future

// ---- Application Graviton/Backend functions ---- //
// Builds new Graviton DB based on input from main()
func NewGravDB(dbFolder, dbmigratewait string) *GravitonStore {
	var Graviton_backend *GravitonStore = &GravitonStore{}

	current_path, err := os.Getwd()
	if err != nil {
		log.Printf("%v\n", err)
	}

	Graviton_backend.DBMigrateWait, _ = time.ParseDuration(dbmigratewait)

	Graviton_backend.DBFolder = dbFolder

	Graviton_backend.DBPath = filepath.Join(current_path, dbFolder)

	Graviton_backend.DB, err = graviton.NewDiskStore(Graviton_backend.DBPath)
	if err != nil {
		log.Fatalf("[NewGravDB] Could not create db store: %v\n", err)
	}

	// Incase we ever need to implement SwapGravDB - an array of dbtrees will be needed to cursor all data since it's at the tree level to ensure proper export/import
	/*
		for i := 0; i < len(dbtrees); i++ {
			Graviton_backend.DBTrees = append(Graviton_backend.DBTrees, dbtrees[i])
		}
	*/

	return Graviton_backend
}

// Stores gnomon's last indexed height - this is for stateful stores on close and reference on open
func (g *GravitonStore) StoreLastIndexHeight(last_indexedheight int64) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	topoheight := strconv.FormatInt(last_indexedheight, 10)

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreLastIndexHeight] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("stats")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreLastIndexHeight] ERROR: Tree is nil for 'stats'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	tree.Put([]byte("lastindexedheight"), []byte(topoheight)) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v\n", cerr)
		return cerr
	}
	return nil
}

// Gets gnomon's last indexed height - this is for stateful stores on close and reference on open
func (g *GravitonStore) GetLastIndexHeight() int64 {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[GetLastIndexHeight] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("stats") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetLastIndexHeight] ERROR: Tree is nil for 'stats'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return 0
		}
	}
	key := "lastindexedheight"

	v, _ := tree.Get([]byte(key))

	if v != nil {
		topoheight, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			log.Printf("ERR - Error parsing stored int for lastindexheight: %v\n", err)
			return 0
		}
		return topoheight
	}

	log.Printf("[GetLastIndexHeight] No last index height. Starting from 0\n")

	return 0
}

// Stores gnomon's txcount by a given txType - this is for stateful stores on close and reference on open
func (g *GravitonStore) StoreTxCount(count int64, txType string) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	txCount := strconv.FormatInt(count, 10)

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreTxCount] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	key := txType + "txcount"

	tree, _ := ss.GetTree("stats")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreTxCount] ERROR: Tree is nil for 'stats'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	tree.Put([]byte(key), []byte(txCount)) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v\n", cerr)
		return cerr
	}
	return nil
}

// Gets gnomon's txcount by a given txType - this is for stateful stores on close and reference on open
func (g *GravitonStore) GetTxCount(txType string) int64 {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[GetTxCount] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("stats") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetTxCount] ERROR: Tree is nil for 'stats'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return 0
		}
	}
	key := txType + "txcount"

	v, _ := tree.Get([]byte(key))

	if v != nil {
		txCount, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			log.Printf("ERR - Error parsing stored int for txcount: %v\n", err)
			return 0
		}
		return txCount
	}

	//log.Printf("[GetTxCount] No txcount stored for '%v'. Starting from 0\n", txType)

	return 0
}

// Stores the owner (who deployed it) of a given scid
func (g *GravitonStore) StoreOwner(scid string, owner string) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreOwner] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("owner")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreOwner] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("owner")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	tree.Put([]byte(scid), []byte(owner)) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v\n", cerr)
		return cerr
	}
	return nil
}

// Returns the owner (who deployed it) of a given scid
func (g *GravitonStore) GetOwner(scid string) string {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[GetOwner] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("owner") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetOwner] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("stats")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return ""
		}
	}
	key := scid

	v, _ := tree.Get([]byte(key))

	if v != nil {
		return string(v)
	}

	log.Printf("[GetOwner] No owner for %v\n", scid)

	return ""
}

// Returns all of the deployed SCIDs with their corresponding owners (who deployed it)
func (g *GravitonStore) GetAllOwnersAndSCIDs() map[string]string {
	results := make(map[string]string)

	store := g.DB
	ss, _ := store.LoadSnapshot(0)
	tree, _ := ss.GetTree("owner")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetAllOwnersAndSCIDs] ERROR: Tree is nil for 'owner'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("owner")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return results
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		results[string(k)] = string(v)
	}

	return results
}

// Stores all normal txs with SCIDs and their respective ring members for future balance/interaction reference
func (g *GravitonStore) StoreNormalTxWithSCIDByAddr(addr string, normTxWithSCID *structures.NormalTXWithSCIDParse) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreSCIDInteractionHeight] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	treename := "normaltxwithscid"
	tree, _ := ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreNormalTxWithSCIDByAddr] ERROR: Tree is nil for 'normaltxwithscid'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("normaltxwithscid")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	key := addr
	currNormTxsWithSCID, err := tree.Get([]byte(key))
	var normTxsWithSCID []*structures.NormalTXWithSCIDParse

	var newNormTxsWithSCID []byte

	if err != nil {
		normTxsWithSCID = append(normTxsWithSCID, normTxWithSCID)
	} else {
		// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currNormTxsWithSCID, &normTxsWithSCID)

		for _, v := range normTxsWithSCID {
			if v.Txid == normTxWithSCID.Txid {
				// Return nil if already exists in array.
				// Clause for this is in event we pop backwards in time and already have this data stored.
				// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
				return nil
			}
		}

		normTxsWithSCID = append(normTxsWithSCID, normTxWithSCID)
	}
	newNormTxsWithSCID, err = json.Marshal(normTxsWithSCID)
	if err != nil {
		return fmt.Errorf("[Graviton] could not marshal normTxsWithSCID info: %v", err)
	}

	tree.Put([]byte(key), newNormTxsWithSCID)

	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v", cerr)
	}
	return nil
}

// Returns all normal txs with SCIDs based on a given address
func (g *GravitonStore) GetAllNormalTxWithSCIDByAddr(addr string) (normTxsWithSCID []*structures.NormalTXWithSCIDParse) {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	treename := "normaltxwithscid"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetAllNormalTxWithSCIDByAddr] ERROR: Tree is nil for 'normaltxwithscid'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("normaltxwithscid")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return normTxsWithSCID
		}
	}
	key := addr

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &normTxsWithSCID)
		return normTxsWithSCID
	}

	return nil
}

// Returns all normal txs with SCIDs based on a given SCID
func (g *GravitonStore) GetAllNormalTxWithSCIDBySCID(scid string) (normTxsWithSCID []*structures.NormalTXWithSCIDParse) {
	var resultset []string
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	treename := "normaltxwithscid"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetAllNormalTxWithSCIDBySCID] ERROR: Tree is nil for 'normaltxwithscid'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("normaltxwithscid")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return normTxsWithSCID
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails []*structures.NormalTXWithSCIDParse
		_ = json.Unmarshal(v, &currdetails)
		for _, cv := range currdetails {
			if cv.Scid == scid && !idExist(resultset, cv.Txid) {
				normTxsWithSCID = append(normTxsWithSCID, cv)
				resultset = append(resultset, cv.Txid)
			}
		}
	}

	return normTxsWithSCID
}

// Check if value exists within a string array/slice
func idExist(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

// Stores all scinvoke details of a given scid
func (g *GravitonStore) StoreInvokeDetails(scid string, signer string, entrypoint string, topoheight int64, invokedetails *structures.SCTXParse) error {
	confBytes, err := json.Marshal(invokedetails)
	if err != nil {
		return fmt.Errorf("[StoreInvokeDetails] could not marshal invokedetails info: %v\n", err)
	}

	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreInvokeDetails] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	// Tree - SCID // (either string or hex - will be []byte in graviton anyways.. may just go with hex)
	// Key - sender:topoheight:entrypoint // (We know that we can have 1 sender per scid per topoheight - do we need entrypoint appended? does it matter?)
	tree, _ := ss.GetTree(scid)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreInvokeDetails] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", scid)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(scid)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	key := signer + ":" + strconv.FormatInt(topoheight, 10) + ":" + entrypoint
	tree.Put([]byte(key), confBytes) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v\n", cerr)
		return cerr
	}
	return nil
}

// Returns all scinvoke calls from a given scid
func (g *GravitonStore) GetAllSCIDInvokeDetails(scid string) (invokedetails []*structures.SCTXParse) {
	store := g.DB
	ss, _ := store.LoadSnapshot(0)
	tree, _ := ss.GetTree(scid)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetAllSCIDInvokeDetails] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", scid)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(scid)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return invokedetails
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails *structures.SCTXParse
		_ = json.Unmarshal(v, &currdetails)
		invokedetails = append(invokedetails, currdetails)
	}

	return invokedetails
}

// Retruns all scinvoke calls from a given scid that match a given entrypoint
func (g *GravitonStore) GetAllSCIDInvokeDetailsByEntrypoint(scid string, entrypoint string) (invokedetails []*structures.SCTXParse) {
	store := g.DB
	ss, _ := store.LoadSnapshot(0)
	tree, _ := ss.GetTree(scid)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetAllSCIDInvokeDetailsByEntrypoint] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", scid)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(scid)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return invokedetails
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails *structures.SCTXParse
		_ = json.Unmarshal(v, &currdetails)
		if currdetails.Entrypoint == entrypoint {
			invokedetails = append(invokedetails, currdetails)
		}
	}

	return invokedetails
}

// Returns all scinvoke calls from a given scid that match a given signer
func (g *GravitonStore) GetAllSCIDInvokeDetailsBySigner(scid string, signer string) (invokedetails []*structures.SCTXParse) {
	store := g.DB
	ss, _ := store.LoadSnapshot(0)
	tree, _ := ss.GetTree(scid)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetAllSCIDInvokeDetailsBySigner] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", scid)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(scid)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return invokedetails
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails *structures.SCTXParse
		_ = json.Unmarshal(v, &currdetails)
		if currdetails.Sender == signer {
			invokedetails = append(invokedetails, currdetails)
		}
	}

	return invokedetails
}

// Stores simple getinfo polling from the daemon
func (g *GravitonStore) StoreGetInfoDetails(getinfo *structures.GetInfo) error {
	confBytes, err := json.Marshal(getinfo)
	if err != nil {
		return fmt.Errorf("[StoreGetInfoDetails] could not marshal getinfo info: %v\n", err)
	}

	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreGetInfoDetails] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("getinfo")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreGetInfoDetails] ERROR: Tree is nil for 'getinfo'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("getinfo")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	key := "getinfo"
	tree.Put([]byte(key), confBytes) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v\n", cerr)
		return cerr
	}
	return nil
}

// Returns simple getinfo polling from the daemon
func (g *GravitonStore) GetGetInfoDetails() *structures.GetInfo {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	var getinfo *structures.GetInfo

	tree, _ := ss.GetTree("getinfo") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetGetInfoDetails] ERROR: Tree is nil for 'getinfo'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("getinfo")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return getinfo
		}
	}
	key := "getinfo"

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &getinfo)
		return getinfo
	}

	return nil
}

// Stores SC variables at a given topoheight (called on any new scdeploy or scinvoke actions)
func (g *GravitonStore) StoreSCIDVariableDetails(scid string, variables []*structures.SCIDVariable, topoheight int64) error {
	confBytes, err := json.Marshal(variables)
	if err != nil {
		return fmt.Errorf("[StoreSCIDVariableDetails] could not marshal getinfo info: %v\n", err)
	}

	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreSCIDVariableDetails] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	treename := scid + "vars"
	tree, _ := ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreSCIDVariableDetails] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", treename)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	key := strconv.FormatInt(topoheight, 10)
	tree.Put([]byte(key), confBytes) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v\n", cerr)
		return cerr
	}
	return nil
}

// Gets SC variables at a given topoheight
func (g *GravitonStore) GetSCIDVariableDetailsAtTopoheight(scid string, topoheight int64) []*structures.SCIDVariable {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	var variables []*structures.SCIDVariable

	treename := scid + "vars"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetSCIDVariableDetailsAtTopoheight] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", treename)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return variables
		}
	}
	key := strconv.FormatInt(topoheight, 10)

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &variables)
		return variables
	}

	return nil
}

// Gets SC variables at all topoheights
func (g *GravitonStore) GetAllSCIDVariableDetails(scid string) map[int64][]*structures.SCIDVariable {
	results := make(map[int64][]*structures.SCIDVariable)

	store := g.DB
	ss, _ := store.LoadSnapshot(0)
	treename := scid + "vars"
	tree, _ := ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetAllSCIDVariableDetails] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", treename)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return results
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		topoheight, _ := strconv.ParseInt(string(k), 10, 64)
		var variables []*structures.SCIDVariable
		_ = json.Unmarshal(v, &variables)
		results[topoheight] = variables
	}

	return results
}

// Gets SC variable keys at given topoheight who's value equates to a given interface{} (string/uint64)
func (g *GravitonStore) GetSCIDKeysByValue(scid string, val interface{}, height int64, rmax bool) (keysstring []string, keysuint64 []uint64) {
	scidInteractionHeights := g.GetSCIDInteractionHeight(scid)

	interactionHeight := g.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := g.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := val.(type) {
	case uint64:
		for _, v := range variables {
			switch cval := v.Value.(type) {
			case uint64:
				if inpvar == cval {
					switch ckey := v.Key.(type) {
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
func (g *GravitonStore) GetSCIDValuesByKey(scid string, key interface{}, height int64, rmax bool) (valuesstring []string, valuesuint64 []uint64) {
	scidInteractionHeights := g.GetSCIDInteractionHeight(scid)

	interactionHeight := g.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := g.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := key.(type) {
	case uint64:
		for _, v := range variables {
			switch ckey := v.Key.(type) {
			case uint64:
				if inpvar == ckey {
					switch cval := v.Value.(type) {
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

// Stores SC interaction height and detail - height invoked upon and type (scinstall/scinvoke). This is separate tree & k/v since we can query it for other things at less data retrieval
func (g *GravitonStore) StoreSCIDInteractionHeight(scid string, height int64) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreSCIDInteractionHeight] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	treename := scid + "heights"
	tree, _ := ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreSCIDInteractionHeight] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", treename)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	key := scid
	currSCIDInteractionHeight, err := tree.Get([]byte(key))
	var interactionHeight []int64

	var newInteractionHeight []byte

	if err != nil {
		interactionHeight = append(interactionHeight, height)
	} else {
		// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currSCIDInteractionHeight, &interactionHeight)

		for _, v := range interactionHeight {
			if v == height {
				// Return nil if already exists in array.
				// Clause for this is in event we pop backwards in time and already have this data stored.
				// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
				return nil
			}
		}
		interactionHeight = append(interactionHeight, height)
	}
	newInteractionHeight, err = json.Marshal(interactionHeight)
	if err != nil {
		return fmt.Errorf("[Graviton] could not marshal interactionHeight info: %v", err)
	}

	tree.Put([]byte(key), newInteractionHeight)

	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v", cerr)
	}
	return nil
}

// Gets SC interaction height and detail by a given SCID
func (g *GravitonStore) GetSCIDInteractionHeight(scid string) []int64 {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	var scidinteractions []int64

	treename := scid + "heights"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetSCIDInteractionHeight] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", treename)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return scidinteractions
		}
	}
	key := scid

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &scidinteractions)
		return scidinteractions
	}

	return nil
}

func (g *GravitonStore) GetInteractionIndex(topoheight int64, heights []int64, rmax bool) (height int64) {
	if len(heights) <= 0 {
		return height
	}

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(heights, func(i, j int) bool {
		return heights[i] > heights[j]
	})

	if topoheight > heights[0] || rmax {
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

// Stores any SCIDs that were attempted to be deployed but not correct - log scid/fees burnt attempting it.
func (g *GravitonStore) StoreInvalidSCIDDeploys(scid string, fee uint64) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreInvalidSCIDDeploys] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	treename := "invalidscids"
	tree, _ := ss.GetTree(treename)
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreInvalidSCIDDeploys] ERROR: Tree is nil for '%v'. Attempting to rollback 1 snapshot\n", treename)
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree(treename)
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	key := "invalid"
	currSCIDInteractionHeight, err := tree.Get([]byte(key))
	currInvalidSCIDs := make(map[string]uint64)

	var newInvalidSCIDs []byte

	if err != nil {
		currInvalidSCIDs[scid] = fee
	} else {
		// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currSCIDInteractionHeight, &currInvalidSCIDs)

		currInvalidSCIDs[scid] = fee
	}
	newInvalidSCIDs, err = json.Marshal(currInvalidSCIDs)
	if err != nil {
		return fmt.Errorf("[Graviton] could not marshal interactionHeight info: %v", err)
	}

	tree.Put([]byte(key), newInvalidSCIDs)

	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v", cerr)
	}
	return nil
}

// Gets any SCIDs that were attempted to be deployed but not correct and their fees
func (g *GravitonStore) GetInvalidSCIDDeploys() map[string]uint64 {
	invalidSCIDs := make(map[string]uint64)

	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	treename := "invalidscids"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetInvalidSCIDDeploys] ERROR: Tree is nil for 'invalidscids'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("invalidscids")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return invalidSCIDs
		}
	}
	key := "invalid"

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &invalidSCIDs)
		return invalidSCIDs
	}

	return nil
}

// Stores the miniblocks within a given blid
func (g *GravitonStore) StoreMiniblockDetailsByHash(blid string, mbldetails []*structures.MBLInfo) error {
	for _, v := range mbldetails {
		err := g.StoreMiniblockCountByAddress(v.Miner)
		if err != nil {
			log.Printf("[Store] ERR - Error adding miniblock count for address '%v'", v.Miner)
		}
	}

	confBytes, err := json.Marshal(mbldetails)
	if err != nil {
		return fmt.Errorf("[StoreMiniblockDetailsByHash] could not marshal getinfo info: %v\n", err)
	}

	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreMiniblockDetailsByHash] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("miniblocks")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreMiniblockDetailsByHash] ERROR: Tree is nil for 'miniblocks'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("miniblocks")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	tree.Put([]byte(blid), confBytes) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v\n", cerr)
		return cerr
	}
	return nil
}

// Returns all miniblock details for synced chain
func (g *GravitonStore) GetAllMiniblockDetails() map[string][]*structures.MBLInfo {
	mbldetails := make(map[string][]*structures.MBLInfo)
	store := g.DB
	ss, _ := store.LoadSnapshot(0)
	tree, _ := ss.GetTree("miniblocks")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetAllMiniblockDetails] ERROR: Tree is nil for 'miniblocks'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("miniblocks")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return mbldetails
		}
	}

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		var currdetails []*structures.MBLInfo
		_ = json.Unmarshal(v, &currdetails)
		mbldetails[string(k)] = currdetails
	}

	return mbldetails
}

// Returns the miniblocks within a given blid if previously stored
func (g *GravitonStore) GetMiniblockDetailsByHash(blid string) []*structures.MBLInfo {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	var miniblocks []*structures.MBLInfo

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[GetMiniblockDetailsByHash] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("miniblocks") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetMiniblockDetailsByHash] ERROR: Tree is nil for 'miniblocks'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("miniblocks")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return miniblocks
		}
	}
	key := blid

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &miniblocks)
		return miniblocks
	}

	return nil
}

// Stores counts of miniblock finders by address
func (g *GravitonStore) StoreMiniblockCountByAddress(addr string) error {
	currCount := g.GetMiniblockCountByAddress(addr)

	// Add 1 to currCount
	currCount++

	confBytes, err := json.Marshal(currCount)
	if err != nil {
		return fmt.Errorf("[StoreMiniblockCountByAddress] could not marshal getinfo info: %v\n", err)
	}

	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreMiniblockCountByAddress] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("blockcount")
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-StoreMiniblockCountByAddress] ERROR: Tree is nil for 'blockcount'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("blockcount")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return terr
		}
	}
	key := addr
	tree.Put([]byte(key), confBytes) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v\n", cerr)
		return cerr
	}
	return nil
}

// Gets counts of miniblock finders by address
func (g *GravitonStore) GetMiniblockCountByAddress(addr string) int64 {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[GetMiniblockDetailsByHash] G is migrating... sleeping for %v...\n", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("blockcount") // use or create tree named by poolhost in config
	// Catch/handle a nil tree. TODO: This should gracefully cause shutdown, if we cannot get the previous snapshot data. Also need to handle losing that snapshot, how do we handle.
	if tree == nil {
		var terr error
		log.Printf("[Graviton-GetMiniblockCountByAddress] ERROR: Tree is nil for 'blockcount'. Attempting to rollback 1 snapshot\n")
		prevss, _ := store.LoadSnapshot(ss.GetVersion() - 1)
		tree, terr = prevss.GetTree("blockcount")
		if tree == nil {
			log.Printf("[Graviton] ERROR: %v\n", terr)
			return 0
		}
	}
	key := addr

	var miniblocks int64

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &miniblocks)
		return miniblocks
	}

	return int64(0)
}

// Gets all SCID interacts from a given address - non-builtin/name scids.
func (g *GravitonStore) GetSCIDInteractionByAddr(addr string) (scids []string) {
	normTxsWithSCID := g.GetAllNormalTxWithSCIDByAddr(addr)

	// Append scids list of normtxs scid interaction
	for _, v := range normTxsWithSCID {
		if !idExist(scids, v.Scid) {
			scids = append(scids, v.Scid)
		}
	}

	allSCIDs := g.GetAllOwnersAndSCIDs()
	for k, _ := range allSCIDs {
		// Skip builtin name registration, no need to waste cursor time on this one since it's not pertinent to goal of function
		// TODO: Future state, we'll have much more SCID interaction and this will get slower and slower, will need to speedup. Probably will happen with data re-org in future
		if k == "0000000000000000000000000000000000000000000000000000000000000001" {
			continue
		}
		invokedetails := g.GetAllSCIDInvokeDetailsBySigner(k, addr)
		if len(invokedetails) > 0 {
			if !idExist(scids, k) {
				scids = append(scids, k)
			}
		}
	}

	return scids
}

// ---- End Application Graviton/Backend functions ---- //
