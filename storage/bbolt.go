package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/civilware/Gnomon/structures"

	bolt "go.etcd.io/bbolt"
)

type BboltStore struct {
	DB      *bolt.DB
	DBPath  string
	Writing int
	Writer  string
	Closing bool
	Buckets []string
}

func NewBBoltDB(dbPath string, dbName string) (*BboltStore, error) {
	var err error
	var Bbolt_backend *BboltStore = &BboltStore{}

	Bbolt_backend.DB, err = bolt.Open(dbName, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return Bbolt_backend, fmt.Errorf("[NewBBoltDB] Coult not create bbolt db store: %v", err)
	}

	Bbolt_backend.DBPath = dbPath

	return Bbolt_backend, err
}

// Stores bbolt's last indexed height - this is for stateful stores on close and reference on open
func (bbs *BboltStore) StoreLastIndexHeight(last_indexedheight int64) (changes bool, err error) {
	bName := "stats"

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		key := "lastindexedheight"
		topoheight := strconv.FormatInt(last_indexedheight, 10)

		err = b.Put([]byte(key), []byte(topoheight))
		changes = true
		return
	})

	return
}

// Gets bbolt's last indexed height - this is for stateful stores on close and reference on open
func (bbs *BboltStore) GetLastIndexHeight() (topoheight int64, err error) {
	bName := "stats"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := "lastindexedheight"
			v := b.Get([]byte(key))

			if v != nil {
				topoheight, err = strconv.ParseInt(string(v), 10, 64)
				if err != nil {
					return fmt.Errorf("[bbs-GetLastIndexHeight] ERR - Error parsing stored int for lastindexheight: %v\n", err)
				}
			}
		}
		return
	})

	if topoheight == 0 {
		log.Printf("[bbs-GetLastIndexHeight] No stored last index height. Starting from 0 or latest if fastsync is enabled\n")
	}

	return
}

// Stores bbolt's txcount by a given txType - this is for stateful stores on close and reference on open
func (bbs *BboltStore) StoreTxCount(count int64, txType string) (changes bool, err error) {
	bName := "stats"

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		key := txType + "txcount"

		txCount := strconv.FormatInt(count, 10)

		err = b.Put([]byte(key), []byte(txCount))
		changes = true
		return
	})

	return
}

// Gets bbolt's txcount by a given txType - this is for stateful stores on close and reference on open
func (bbs *BboltStore) GetTxCount(txType string) (txCount int64) {
	bName := "stats"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := txType + "txcount"
			v := b.Get([]byte(key))

			if v != nil {
				txCount, err = strconv.ParseInt(string(v), 10, 64)
				if err != nil {
					return fmt.Errorf("[bbs-GetLastIndexHeight] ERR - Error parsing stored int for txcount: %v\n", err)
				}
			}
		}
		return
	})

	return
}

// Stores the owner (who deployed it) of a given scid
func (bbs *BboltStore) StoreOwner(scid string, owner string) (changes bool, err error) {
	bName := "scowner"

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Put([]byte(scid), []byte(owner))
		changes = true
		return
	})

	return
}

// Returns the owner (who deployed it) of a given scid
func (bbs *BboltStore) GetOwner(scid string) string {
	var v []byte
	bName := "scowner"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := scid
			v = b.Get([]byte(key))
		}

		return
	})

	if v != nil {
		return string(v)
	}

	log.Printf("[GetOwner] No owner for %v\n", scid)

	return ""
}

// Returns all of the deployed SCIDs with their corresponding owners (who deployed it)
func (bbs *BboltStore) GetAllOwnersAndSCIDs() map[string]string {
	results := make(map[string]string)

	bName := "scowner"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			c := b.Cursor()

			for k, v := c.First(); err == nil; k, v = c.Next() {
				if k != nil && v != nil {
					results[string(k)] = string(v)
				} else {
					break
				}
			}
		}

		return
	})

	return results
}

// Stores all normal txs with SCIDs and their respective ring members for future balance/interaction reference
func (bbs *BboltStore) StoreNormalTxWithSCIDByAddr(addr string, normTxWithSCID *structures.NormalTXWithSCIDParse) (changes bool, err error) {
	var newNormTxsWithSCID []byte
	var currNormTxsWithSCID []byte
	var normTxsWithSCID []*structures.NormalTXWithSCIDParse
	bName := "normaltxwithscid"
	key := addr

	err = bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			currNormTxsWithSCID = b.Get([]byte(key))
		}
		return
	})

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		if currNormTxsWithSCID == nil {
			normTxsWithSCID = append(normTxsWithSCID, normTxWithSCID)
		} else {
			// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
			_ = json.Unmarshal(currNormTxsWithSCID, &normTxsWithSCID)

			for _, v := range normTxsWithSCID {
				if v.Txid == normTxWithSCID.Txid {
					// Return nil if already exists in array.
					// Clause for this is in event we pop backwards in time and already have this data stored.
					// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
					return
				}
			}

			normTxsWithSCID = append(normTxsWithSCID, normTxWithSCID)
		}
		newNormTxsWithSCID, err = json.Marshal(normTxsWithSCID)
		if err != nil {
			return fmt.Errorf("[BBolt] could not marshal normTxsWithSCID info: %v", err)
		}

		err = b.Put([]byte(key), newNormTxsWithSCID)
		changes = true
		return
	})

	return
}

// Returns all normal txs with SCIDs based on a given address
func (bbs *BboltStore) GetAllNormalTxWithSCIDByAddr(addr string) (normTxsWithSCID []*structures.NormalTXWithSCIDParse) {
	bName := "normaltxwithscid"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := addr
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &normTxsWithSCID)
			}
		}
		return
	})

	return
}

// Returns all normal txs with SCIDs based on a given SCID
func (bbs *BboltStore) GetAllNormalTxWithSCIDBySCID(scid string) (normTxsWithSCID []*structures.NormalTXWithSCIDParse) {
	var resultset []string

	bName := "normaltxwithscid"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails []*structures.NormalTXWithSCIDParse
					_ = json.Unmarshal(v, &currdetails)
					for _, cv := range currdetails {
						if cv.Scid == scid && !idExist(resultset, cv.Txid) {
							normTxsWithSCID = append(normTxsWithSCID, cv)
							resultset = append(resultset, cv.Txid)
						}
					}
				} else {
					break
				}
			}
		}

		return
	})

	return
}

// Stores all scinvoke details of a given scid
func (bbs *BboltStore) StoreInvokeDetails(scid string, signer string, entrypoint string, topoheight int64, invokedetails *structures.SCTXParse) (changes bool, err error) {
	confBytes, err := json.Marshal(invokedetails)
	if err != nil {
		return changes, fmt.Errorf("[StoreInvokeDetails] could not marshal invokedetails info: %v", err)
	}

	bName := scid

	key := signer + ":" + strconv.FormatInt(topoheight, 10) + ":" + entrypoint

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Put([]byte(key), confBytes)
		changes = true
		return
	})

	return
}

// Returns all scinvoke calls from a given scid
func (bbs *BboltStore) GetAllSCIDInvokeDetails(scid string) (invokedetails []*structures.SCTXParse) {
	bName := scid

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails *structures.SCTXParse
					_ = json.Unmarshal(v, &currdetails)
					invokedetails = append(invokedetails, currdetails)
				} else {
					break
				}
			}
		}

		return
	})

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(invokedetails, func(i, j int) bool {
		return invokedetails[i].Height < invokedetails[j].Height
	})

	return invokedetails
}

// Retruns all scinvoke calls from a given scid that match a given entrypoint
func (bbs *BboltStore) GetAllSCIDInvokeDetailsByEntrypoint(scid string, entrypoint string) (invokedetails []*structures.SCTXParse) {
	bName := scid

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails *structures.SCTXParse
					_ = json.Unmarshal(v, &currdetails)
					if currdetails.Entrypoint == entrypoint {
						invokedetails = append(invokedetails, currdetails)
					}
				} else {
					break
				}
			}
		}

		return
	})

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(invokedetails, func(i, j int) bool {
		return invokedetails[i].Height < invokedetails[j].Height
	})

	return invokedetails
}

// Returns all scinvoke calls from a given scid that match a given signer
func (bbs *BboltStore) GetAllSCIDInvokeDetailsBySigner(scid string, signerPart string) (invokedetails []*structures.SCTXParse) {
	bName := scid

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails *structures.SCTXParse
					_ = json.Unmarshal(v, &currdetails)
					split := strings.Split(currdetails.Sender, signerPart)
					if len(split) > 1 {
						invokedetails = append(invokedetails, currdetails)
					}
				} else {
					break
				}
			}
		}

		return
	})

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(invokedetails, func(i, j int) bool {
		return invokedetails[i].Height < invokedetails[j].Height
	})

	return invokedetails
}

// Stores simple getinfo polling from the daemon
func (bbs *BboltStore) StoreGetInfoDetails(getinfo *structures.GetInfo) (changes bool, err error) {
	confBytes, err := json.Marshal(getinfo)
	if err != nil {
		return changes, fmt.Errorf("[StoreGetInfoDetails] could not marshal getinfo info: %v", err)
	}

	bName := "getinfo"

	key := "getinfo"

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Put([]byte(key), confBytes)
		changes = true
		return
	})

	return
}

// Returns simple getinfo polling from the daemon
func (bbs *BboltStore) GetGetInfoDetails() (getinfo *structures.GetInfo) {
	var v []byte
	bName := "getinfo"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := "getinfo"
			v = b.Get([]byte(key))
		}

		return
	})

	if v != nil {
		_ = json.Unmarshal(v, &getinfo)
		return
	}

	return
}

// Stores SC variables at a given topoheight (called on any new scdeploy or scinvoke actions)
func (bbs *BboltStore) StoreSCIDVariableDetails(scid string, variables []*structures.SCIDVariable, topoheight int64) (changes bool, err error) {
	confBytes, err := json.Marshal(variables)
	if err != nil {
		return changes, fmt.Errorf("[StoreSCIDVariableDetails] could not marshal getinfo info: %v", err)
	}

	bName := scid + "vars"

	key := strconv.FormatInt(topoheight, 10)

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Put([]byte(key), confBytes)
		changes = true
		return
	})

	return
}

// Gets SC variables at a given topoheight
func (bbs *BboltStore) GetSCIDVariableDetailsAtTopoheight(scid string, topoheight int64) (variables []*structures.SCIDVariable) {
	var v []byte
	bName := scid + "vars"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := strconv.FormatInt(topoheight, 10)
			v = b.Get([]byte(key))
		}

		return
	})

	if v != nil {
		_ = json.Unmarshal(v, &variables)
		return
	}

	return
}

// Gets SC variables at all topoheights
func (bbs *BboltStore) GetAllSCIDVariableDetails(scid string) map[int64][]*structures.SCIDVariable {
	results := make(map[int64][]*structures.SCIDVariable)

	bName := scid + "vars"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for k, v := c.First(); err == nil; k, v = c.Next() {
				if k != nil && v != nil {
					topoheight, _ := strconv.ParseInt(string(k), 10, 64)
					var variables []*structures.SCIDVariable
					_ = json.Unmarshal(v, &variables)
					results[topoheight] = variables
				} else {
					break
				}
			}
		}

		return
	})

	return results
}

// Gets SC variable keys at given topoheight who's value equates to a given interface{} (string/uint64)
func (bbs *BboltStore) GetSCIDKeysByValue(scid string, val interface{}, height int64, rmax bool) (keysstring []string, keysuint64 []uint64) {
	scidInteractionHeights := bbs.GetSCIDInteractionHeight(scid)

	interactionHeight := bbs.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := bbs.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := val.(type) {
	case uint64:
		for _, v := range variables {
			switch cval := v.Value.(type) {
			case float64:
				if inpvar == uint64(cval) {
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
func (bbs *BboltStore) GetSCIDValuesByKey(scid string, key interface{}, height int64, rmax bool) (valuesstring []string, valuesuint64 []uint64) {
	scidInteractionHeights := bbs.GetSCIDInteractionHeight(scid)

	interactionHeight := bbs.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := bbs.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := key.(type) {
	case uint64:
		for _, v := range variables {
			switch ckey := v.Key.(type) {
			case float64:
				if inpvar == uint64(ckey) {
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

// Stores SC interaction height and detail - height invoked upon and type (scinstall/scinvoke). This is separate tree & k/v since we can query it for other things at less data retrieval
func (bbs *BboltStore) StoreSCIDInteractionHeight(scid string, height int64) (changes bool, err error) {
	var currSCIDInteractionHeight []byte
	var interactionHeight []int64
	var newInteractionHeight []byte
	bName := scid + "heights"
	key := scid

	err = bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			currSCIDInteractionHeight = b.Get([]byte(key))
		}
		return
	})

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		if currSCIDInteractionHeight == nil {
			interactionHeight = append(interactionHeight, height)
		} else {
			// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
			_ = json.Unmarshal(currSCIDInteractionHeight, &interactionHeight)

			for _, v := range interactionHeight {
				if v == height {
					// Return nil if already exists in array.
					// Clause for this is in event we pop backwards in time and already have this data stored.
					// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
					return
				}
			}

			interactionHeight = append(interactionHeight, height)
		}
		newInteractionHeight, err = json.Marshal(interactionHeight)
		if err != nil {
			return fmt.Errorf("[BBolt] could not marshal interactionHeight info: %v", err)
		}

		err = b.Put([]byte(key), newInteractionHeight)
		changes = true
		return
	})

	return
}

// Gets SC interaction height and detail by a given SCID
func (bbs *BboltStore) GetSCIDInteractionHeight(scid string) (scidinteractions []int64) {
	bName := scid + "heights"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := scid
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &scidinteractions)
			}
		}
		return
	})

	return
}

func (bbs *BboltStore) GetInteractionIndex(topoheight int64, heights []int64, rmax bool) (height int64) {
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
func (bbs *BboltStore) StoreInvalidSCIDDeploys(scid string, fee uint64) (changes bool, err error) {
	var currSCIDInteractionHeight []byte

	currInvalidSCIDs := make(map[string]uint64)
	var newInvalidSCIDs []byte

	bName := "invalidscids"
	key := "invalid"

	err = bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			currSCIDInteractionHeight = b.Get([]byte(key))
		}
		return
	})

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		if currSCIDInteractionHeight == nil {
			currInvalidSCIDs[scid] = fee
		} else {
			// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
			_ = json.Unmarshal(currSCIDInteractionHeight, &currInvalidSCIDs)

			currInvalidSCIDs[scid] = fee
		}
		newInvalidSCIDs, err = json.Marshal(currInvalidSCIDs)
		if err != nil {
			return fmt.Errorf("[bbs-StoreInvalidSCIDDeploys] could not marshal interactionHeight info: %v", err)
		}

		err = b.Put([]byte(key), newInvalidSCIDs)
		changes = true
		return
	})

	return
}

// Gets any SCIDs that were attempted to be deployed but not correct and their fees
func (bbs *BboltStore) GetInvalidSCIDDeploys() map[string]uint64 {
	invalidSCIDs := make(map[string]uint64)

	bName := "invalidscids"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := "invalid"
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &invalidSCIDs)
			}
		}
		return
	})

	return invalidSCIDs
}

// Stores the miniblocks within a given blid
func (bbs *BboltStore) StoreMiniblockDetailsByHash(blid string, mbldetails []*structures.MBLInfo) (changes bool, err error) {
	for _, v := range mbldetails {
		_, err := bbs.StoreMiniblockCountByAddress(v.Miner)
		if err != nil {
			log.Printf("[Store] ERR - Error adding miniblock count for address '%v'", v.Miner)
		}
	}

	confBytes, err := json.Marshal(mbldetails)
	if err != nil {
		return changes, fmt.Errorf("[StoreMiniblockDetailsByHash] could not marshal getinfo info: %v", err)
	}

	bName := "miniblocks"

	key := blid

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Put([]byte(key), confBytes)
		changes = true
		return
	})

	return
}

// Returns all miniblock details for synced chain
func (bbs *BboltStore) GetAllMiniblockDetails() map[string][]*structures.MBLInfo {
	mbldetails := make(map[string][]*structures.MBLInfo)

	bName := "miniblocks"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for k, v := c.First(); err == nil; k, v = c.Next() {
				if k != nil && v != nil {
					var currdetails []*structures.MBLInfo
					_ = json.Unmarshal(v, &currdetails)
					mbldetails[string(k)] = currdetails
				} else {
					break
				}
			}
		}

		return
	})

	return mbldetails
}

// Returns the miniblocks within a given blid if previously stored
func (bbs *BboltStore) GetMiniblockDetailsByHash(blid string) (miniblocks []*structures.MBLInfo) {
	bName := "miniblocks"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := blid
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &miniblocks)
			}
		}
		return
	})

	return
}

// Stores counts of miniblock finders by address
func (bbs *BboltStore) StoreMiniblockCountByAddress(addr string) (changes bool, err error) {
	currCount := bbs.GetMiniblockCountByAddress(addr)

	// Add 1 to currCount
	currCount++

	confBytes, err := json.Marshal(currCount)
	if err != nil {
		return changes, fmt.Errorf("[StoreMiniblockCountByAddress] could not marshal getinfo info: %v", err)
	}

	bName := "blockcount"

	key := addr

	err = bbs.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Put([]byte(key), confBytes)
		changes = true
		return
	})

	return
}

// Gets counts of miniblock finders by address
func (bbs *BboltStore) GetMiniblockCountByAddress(addr string) (miniblocks int64) {
	bName := "blockcount"

	bbs.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := addr
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &miniblocks)
			}
		}
		return
	})

	return
}

// Gets all SCID interacts from a given address - non-builtin/name scids.
func (bbs *BboltStore) GetSCIDInteractionByAddr(addr string) (scids []string) {
	normTxsWithSCID := bbs.GetAllNormalTxWithSCIDByAddr(addr)

	// Append scids list of normtxs scid interaction
	for _, v := range normTxsWithSCID {
		if !idExist(scids, v.Scid) {
			scids = append(scids, v.Scid)
		}
	}

	allSCIDs := bbs.GetAllOwnersAndSCIDs()
	for k, _ := range allSCIDs {
		// Skip builtin name registration, no need to waste cursor time on this one since it's not pertinent to goal of function
		// TODO: Future state, we'll have much more SCID interaction and this will get slower and slower, will need to speedup. Probably will happen with data re-org in future
		if k == "0000000000000000000000000000000000000000000000000000000000000001" {
			continue
		}
		invokedetails := bbs.GetAllSCIDInvokeDetailsBySigner(k, addr)
		if len(invokedetails) > 0 {
			if !idExist(scids, k) {
				scids = append(scids, k)
			}
		}
	}

	return scids
}