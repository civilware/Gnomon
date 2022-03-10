package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
}

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

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for k, v, err := c.First(); err == nil; k, v, err = c.Next() {
		results[string(k)] = string(v)
	}

	return results
}

// Stores all scinvoke details of a given scid
func (g *GravitonStore) StoreInvokeDetails(scid string, signer string, entrypoint string, topoheight int64, invokedetails *structures.Parse) error {
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
func (g *GravitonStore) GetAllSCIDInvokeDetails(scid string) (invokedetails []*structures.Parse) {
	store := g.DB
	ss, _ := store.LoadSnapshot(0)
	tree, _ := ss.GetTree(scid)

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails *structures.Parse
		_ = json.Unmarshal(v, &currdetails)
		invokedetails = append(invokedetails, currdetails)
	}

	return invokedetails
}

// Retruns all scinvoke calls from a given scid that match a given entrypoint
func (g *GravitonStore) GetAllSCIDInvokeDetailsByEntrypoint(scid string, entrypoint string) (invokedetails []*structures.Parse) {
	store := g.DB
	ss, _ := store.LoadSnapshot(0)
	tree, _ := ss.GetTree(scid)

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails *structures.Parse
		_ = json.Unmarshal(v, &currdetails)
		if currdetails.Entrypoint == entrypoint {
			invokedetails = append(invokedetails, currdetails)
		}
	}

	return invokedetails
}

// Returns all scinvoke calls from a given scid that match a given signer
func (g *GravitonStore) GetAllSCIDInvokeDetailsBySigner(scid string, signer string) (invokedetails []*structures.Parse) {
	store := g.DB
	ss, _ := store.LoadSnapshot(0)
	tree, _ := ss.GetTree(scid)

	c := tree.Cursor()
	// Duplicate the LATEST (snapshot 0) to the new DB, this starts the DB over again, but still retaining X number of old DBs for version in future use cases. Here we get the vals before swapping to new db in mem
	for _, v, err := c.First(); err == nil; _, v, err = c.Next() {
		var currdetails *structures.Parse
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

	tree, _ := ss.GetTree("getinfo") // use or create tree named by poolhost in config
	key := "getinfo"

	var getinfo *structures.GetInfo

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

	treename := scid + "vars"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	key := strconv.FormatInt(topoheight, 10)

	var variables []*structures.SCIDVariable

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

// Stores SC interaction height and detail - height invoked upon and type (scinstall/scinvoke). This is separate tree & k/v since we can query it for other things at less data retrieval
func (g *GravitonStore) StoreSCIDInteractionHeight(scid string, interactiontype string, height int64) error {
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
	key := scid
	currSCIDInteractionHeight, err := tree.Get([]byte(key))
	var interactionHeight *structures.SCIDInteractionHeight

	var newInteractionHeight []byte

	if err != nil {
		heightArr := make(map[int64]string)
		heightArr[height] = interactiontype
		interactionHeight = &structures.SCIDInteractionHeight{Heights: heightArr}
	} else {
		// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currSCIDInteractionHeight, &interactionHeight)

		interactionHeight.Heights[height] = interactiontype
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
func (g *GravitonStore) GetSCIDInteractionHeight(scid string) *structures.SCIDInteractionHeight {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	treename := scid + "heights"
	tree, _ := ss.GetTree(treename) // use or create tree named by poolhost in config
	key := scid

	var scidinteractions *structures.SCIDInteractionHeight

	v, _ := tree.Get([]byte(key))

	if v != nil {
		_ = json.Unmarshal(v, &scidinteractions)
		return scidinteractions
	}

	return nil
}

// ---- End Application Graviton/Backend functions ---- //
