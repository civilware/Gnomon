package mbllookup

import (
	"log"
	"os"
	"path/filepath"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/blockchain"
	"github.com/deroproject/derohe/config"
	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/graviton"

	"github.com/civilware/Gnomon/structures"
)

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

var Connected bool
var DeroDB = &Derodbstore{}

var chain *blockchain.Blockchain

func GetMBLByBLHash(bl block.Block) (mblinfo []*structures.MBLInfo, err error) {
	var ss *graviton.Snapshot
	DeroDB.LoadDeroDB()
	ss, err = DeroDB.Balance_store.LoadSnapshot(0)
	if err != nil {
		log.Printf("Err loading snapshot - %v", err)
		return mblinfo, err
	}
	balance_tree, err := ss.GetTree(config.BALANCE_TREE)
	if err != nil {
		log.Printf("Error getting balance tree - %v", err)
		return mblinfo, err
	}

	for _, v := range bl.MiniBlocks {
		if !v.Final {

			_, key_compressed, _, err := balance_tree.GetKeyValueFromHash(v.KeyHash[:16])

			var acckey crypto.Point
			err = acckey.DecodeCompressed(key_compressed[:])
			if err != nil {
				log.Printf("Err decoding key_compressed")
				return mblinfo, err
			}
			astring := rpc.NewAddressFromKeys(&acckey)

			//currHash := v.GetHash().String()
			//currMiner := astring.String()

			//currMBL := &structures.MBLInfo{Hash: currHash, Miner: currMiner}
			//mblinfo.Miniblocks = append(mblinfo.Miniblocks, currMBL)
			mblinfo = append(mblinfo, &structures.MBLInfo{Hash: v.GetHash().String(), Miner: astring.String()})
			//mblinfo[k].Hash = v.GetHash().String()
			//mblinfo[k].Miner = astring.String()
		}
	}

	return mblinfo, nil
}

// ---- Start DERO DB functions ---- //

func (s *Derodbstore) LoadDeroDB() (err error) {
	// Temp defining for now to same directory as testnet folder - TODO: see if we can natively pull in storage location? I doubt it..
	current_path, err := os.Getwd()
	current_path = filepath.Join(current_path, "mainnet")

	current_path = filepath.Join(current_path, "balances")

	_, err = os.Stat(current_path)
	if os.IsNotExist(err) {
		log.Printf("Err - Cannot open store: %v\n", err)
		log.Printf("Err - with 'enable-miniblock-lookup' set to true, be sure to run this from a directory with a full node!")
		return err
	}

	if s.Balance_store, err = graviton.NewDiskStore(current_path); err == nil {
		if err = s.Topo_store.Open(current_path); err == nil {
			s.Block_tx_store.Basedir = current_path
		}
	}

	if err != nil {
		log.Printf("Err - Cannot open store: %v\n", err)
		log.Printf("Err - with 'enable-miniblock-lookup' set to true, be sure to run this from a directory with a full node!")
		return err
	}
	//log.Printf("Initialized: %v\n", current_path)

	return nil
}

func (s *Storetopofs) Open(basedir string) (err error) {
	s.Topomapping, err = os.OpenFile(filepath.Join(basedir, "topo.map"), os.O_RDWR|os.O_CREATE, 0700)
	return err
}

// ---- End DERO DB functions ---- //
