package storage

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// ---- DERO DB functions ---- //
type Derodbstore struct {
	Balance_store  *graviton.Store // stores most critical data, only history can be purged, its merkle tree is stored in the block
	Block_tx_store Storefs         // stores blocks which can be discarded at any time(only past but keep recent history for rollback)
	Topo_store     Storetopofs     // stores topomapping which can only be discarded by punching holes in the start of the file
}

type Storefs struct {
	Basedir string
}

type Storetopofs struct {
	Topomapping *os.File
}

func (s *Derodbstore) LoadDeroDB() (err error) {
	// Temp defining for now to same directory as testnet folder - TODO: see if we can natively pull in storage location? I doubt it..
	current_path, err := os.Getwd()
	current_path = filepath.Join(current_path, "testnet")

	current_path = filepath.Join(current_path, "balances")

	if s.Balance_store, err = graviton.NewDiskStore(current_path); err == nil {
		if err = s.Topo_store.Open(current_path); err == nil {
			s.Block_tx_store.Basedir = current_path
		}
	}

	if err != nil {
		log.Printf("Err - Cannot open store: %v", err)
		return err
	}
	log.Printf("Initialized: %v", current_path)

	return nil
}

func (s *Storetopofs) Open(basedir string) (err error) {
	s.Topomapping, err = os.OpenFile(filepath.Join(basedir, "topo.map"), os.O_RDWR|os.O_CREATE, 0700)
	return err
}

// Different storefs pointers, thus used exported function within. Reference: https://github.com/deroproject/derohe/blob/main/blockchain/storefs.go#L124
func (s *Storefs) ReadBlockSnapshotVersion(h [32]byte) (uint64, error) {
	dir := filepath.Join(filepath.Join(s.Basedir, "bltx_store"), fmt.Sprintf("%02x", h[0]), fmt.Sprintf("%02x", h[1]), fmt.Sprintf("%02x", h[2]))

	files, err := os.ReadDir(dir) // this always returns the sorted list
	if err != nil {
		return 0, err
	}
	// windows has a caching issue, so earlier versions may exist at the same time
	// so we mitigate it, by using the last version, below 3 lines reverse the already sorted arrray
	for left, right := 0, len(files)-1; left < right; left, right = left+1, right-1 {
		files[left], files[right] = files[right], files[left]
	}

	filename_start := fmt.Sprintf("%x.block", h[:])
	for _, file := range files {
		if strings.HasPrefix(file.Name(), filename_start) {
			var ssversion uint64
			parts := strings.Split(file.Name(), "_")
			if len(parts) != 4 {
				panic("such filename cannot occur")
			}
			_, err := fmt.Sscan(parts[2], &ssversion)
			if err != nil {
				return 0, err
			}
			return ssversion, nil
		}
	}

	return 0, os.ErrNotExist
}

// ---- End DERO DB functions ---- //

// ---- Application Graviton/Backend functions ---- //
// Builds new Graviton DB based on input from main()
func NewGravDB(dbtrees []string, dbFolder, dbmigratewait string) *GravitonStore {
	var Graviton_backend *GravitonStore = &GravitonStore{}

	current_path, err := os.Getwd()
	if err != nil {
		log.Printf("%v", err)
	}

	Graviton_backend.DBMigrateWait, _ = time.ParseDuration(dbmigratewait)

	Graviton_backend.DBFolder = dbFolder

	Graviton_backend.DBPath = filepath.Join(current_path, dbFolder)

	Graviton_backend.DB, err = graviton.NewDiskStore(Graviton_backend.DBPath)
	if err != nil {
		log.Fatalf("[NewGravDB] Could not create db store: %v", err)
	}

	// Incase we ever need to implement SwapGravDB - an array of dbtrees will be needed to cursor all data since it's at the tree level to ensure proper export/import
	for i := 0; i < len(dbtrees); i++ {
		Graviton_backend.DBTrees = append(Graviton_backend.DBTrees, dbtrees[i])
	}

	return Graviton_backend
}

func (g *GravitonStore) StoreLastIndexHeight(last_indexedheight int64) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	topoheight := strconv.FormatInt(last_indexedheight, 10)

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreLastIndexHeight] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("stats")
	tree.Put([]byte("lastindexedheight"), []byte(topoheight)) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v", cerr)
		return cerr
	}
	return nil
}

func (g *GravitonStore) GetLastIndexHeight() int64 {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[GetLastIndexHeight] G is migrating... sleeping for %v...", g.DBMigrateWait)
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
			log.Printf("ERR - Error parsing stored int for lastindexheight: %v", err)
			return 0
		}
		return topoheight
	}

	log.Printf("[GetLastIndexHeight] No last index height. Starting from 0")

	return 0
}

func (g *GravitonStore) StoreOwner(scid string, owner string) error {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[StoreOwner] G is migrating... sleeping for %v...", g.DBMigrateWait)
		time.Sleep(g.DBMigrateWait)
		store = g.DB
		ss, _ = store.LoadSnapshot(0) // load most recent snapshot
	}

	tree, _ := ss.GetTree("owner")
	tree.Put([]byte(scid), []byte(owner)) // insert a value
	_, cerr := graviton.Commit(tree)
	if cerr != nil {
		log.Printf("[Graviton] ERROR: %v", cerr)
		return cerr
	}
	return nil
}

func (g *GravitonStore) GetOwner(scid string) string {
	store := g.DB
	ss, _ := store.LoadSnapshot(0) // load most recent snapshot

	// Swap DB at g.DBMaxSnapshot+ commits. Check for g.migrating, if so sleep for g.DBMigrateWait ms
	for g.migrating == 1 {
		log.Printf("[GetOwner] G is migrating... sleeping for %v...", g.DBMigrateWait)
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

	log.Printf("[GetOwner] No owner for %v", scid)

	return ""
}

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

// ---- End Application Graviton/Backend functions ---- //
