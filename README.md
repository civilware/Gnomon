![](https://github.com/civilware/Gnomon/blob/main/gnomon.png?raw=true)

## Gnomon <!-- omit in toc -->
### Decentralized Search Engine <!-- omit in toc -->

Gnomon is an open source decentralized search engine built on the DERO Decentralized Application Platform. It aims to provide easy access to blockchain data by reading and indexing data received directly from the connected node (daemon). By allowing user provided search terms, Gnomon can query and index any resulting data for use in decentralized applications and more. There are no trusted third-parties, tracking services, or cloud servers that stand between you and your data.

### Core Features <!-- omit in toc -->

* Smart Contract querying and indexing
* Transaction querying and indexing
* Transaction Markup Language (xtag) support
* No custom daemon, no modified code.

<br>
Built for the masses.

<br>

## Table of Contents <!-- omit in toc -->
- [Contributing](#contributing)
- [Using Gnomon As A Package](#using-gnomon-as-a-package)
  - [Search Filter(s)](#search-filters)
  - [Setting Up Database](#setting-up-database)
    - [BoltDB](#boltdb)
    - [GravitonDB](#gravitondb)
  - [Defining Your Indexer(s)](#defining-your-indexers)
  - [Reading From DB(s)](#reading-from-dbs)
    - [BoltDB](#boltdb-1)
    - [GravitonDB](#gravitondb-1)
  - [Defining API(s)](#defining-apis)
- [GnomonIndexer](#gnomonindexer)
- [GnomonSC Index Service](#gnomonsc-index-service)
  - [Running the GnomonSC Index Service](#running-the-gnomonsc-index-service)

## Contributing
Bug fixes, feature requests etc. can be submitted through normal issues on this repository. Feel free to follow the [Pull Request Template](./.github/pull_request_template.md) for any code merges.

## Using Gnomon As A Package
Gnomon is written with the expectation that the primary use case would be consuming it as a go package. The basis is to be able to leverage it for your own dApps or other configurations which may need to track specific contracts or data and use it appropriately. You can also use it as a [standalone command line interface](#gnomonindexer).

### Search Filter(s)
```go
// Search filter - can be "" for any/everything, or specific to a piece of code in your template.bas files etc.
search_filter := "Function InitializePrivate() Uint64"

// You can also define multiple with the ';;;' separator
search_filter := "InitializePrivate;;;SEND_DERO_TO_ADDRESS;;;SEND_ASSET_TO_ADDRESS"
```

### Setting Up Database
#### BoltDB
```go
import "github.com/civilware/Gnomon/storage"
...

var Graviton_backend *storage.GravitonStore
var Bbs_backend *storage.BboltStore

// Database
var shasum string
shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))  // shasum can be used to unique a given db directory if you choose, can use normal words as well - whatever your 

db_name := fmt.Sprintf("%s_%s.db", "GNOMON", shasum)
wd, err := os.Getwd()
if err != nil {
  logger.Fatalf("[Main] Err getting working directory: %v", err)
}
db_path := filepath.Join(wd, "gnomondb")
Bbs_backend, err = storage.NewBBoltDB(db_path, db_name)
if err != nil {
  logger.Fatalf("[Main] Err creating boltdb: %v", err)
}
```

#### GravitonDB
```go
import "github.com/civilware/Gnomon/storage"
...

var Graviton_backend *storage.GravitonStore
var Bbs_backend *storage.BboltStore

// Database
var shasum string
shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))  // shasum can be used to unique a given db directory if you choose, can use normal words as well - whatever your choices.
db_folder := fmt.Sprintf("gnomondb\\%s_%s", "GNOMON", shasum)   // directory in current working directory. New gnomondb\GNOMON_shasum defined
Graviton_backend := storage.NewGravDB(db_folder, "25ms")
```

### Defining Your Indexer(s)
```go
import "github.com/civilware/Gnomon/indexer"
...

// dbtype defines what type of backend db will be used for storage. boltdb being the default and gravitondb being another option.
dbtype := "boltdb"

// Last IndexedHeight - This can be used in addition to the db store for picking up where you left off. 
// Graviton_backend.StoreLastIndexHeight(LastIndexedHeight)  - either on-close or in a channel interval etc.
// Good thing about chain data, even if you didn't store, worst that'll happen is it re-scans and stores same info over itself
last_indexedheight := int64(1)

// Daemon Endpoint - 127.0.0.1:10102 (local) or remote IP etc. Change port if using testnet (i.e. 40402)
daemon_endpoint := "127.0.0.1:10102"

// RunMode - Daemon and Wallet. As of this release Daemon is the only one doing any actions - wallet will be future
runmode := "daemon"

// mbl - More of a 'bonus' feature - tracks/stores miniblock propogation on-chain as blocks are scanned. Boolean value to enable/disable this
// NOTE - This REQUIRES the same directory as your  DERODB etc. I'd suggest checking out binary implementation of this and adjust accordingly, most likely not used through packaging (but could be)
mbl := false

// closeondisconnect - More of a 'specific' use case feature - if daemon connectivity (after previously being connected) ceases for x time, then close the indexer which eventually panics. Will be cleaner in future or removed with upstream daemons being pooled. Primary use case is to ensure api is disconnected when daemon is not connected for bad data, could accomplish other ways.
closeondisconnect := false

// fastsync - Syncs against gnomon scid for scids and compares against search_filter, starts you at current topoheight
fastsync := false

// logrus logging init (optional)
indexer.InitLog(arguments, os.Stdout)

// Indexer
defaultIndexer := indexer.NewIndexer(Graviton_backend, Bbs_backend, dbtype, search_filter, last_indexedheight, daemon_endpoint, runmode, mbl, closeondisconnect, fastsync)
```

### Reading From DB(s)
#### BoltDB
[boltdb](https://github.com/etcd-io/bbolt)
```go
import "github.com/civilware/Gnomon/storage"
...

// Examples:
// Return search_filter validated SCIDs and their respective owners (if it could be parsed [ringsize = 2])
validatedSCIDs := Bbs_backend.GetAllOwnersAndSCIDs()

// Get last indexed height for continuing along
last_indexedheight := Bbs_backend.GetLastIndexHeight()

// Get all SC transfer txs that are 'normal' transfers by a given address
allNormalTxWithSCID := Bbs_backend.GetAllNormalTxWithSCIDByAddr("address")

// Get all SCID invokes 
allSCIDInvokes := Bbs_backend.GetAllSCIDInvokeDetails("scid")

// And so on... get functions within bbolt.go.go
```

#### GravitonDB
[Graviton](https://github.com/deroproject/graviton)
```go
import "github.com/civilware/Gnomon/storage"
...

// Examples:
// Return search_filter validated SCIDs and their respective owners (if it could be parsed [ringsize = 2])
validatedSCIDs := Graviton_backend.GetAllOwnersAndSCIDs()

// Get last indexed height for continuing along
last_indexedheight := Graviton_backend.GetLastIndexHeight()

// Get all SC transfer txs that are 'normal' transfers by a given address
allNormalTxWithSCID := Graviton_backend.GetAllNormalTxWithSCIDByAddr("address")

// Get all SCID invokes 
allSCIDInvokes := Graviton_backend.GetAllSCIDInvokeDetails("scid")

// And so on... get functions within gravdb.go
```

### Defining API(s)
```go
import "github.com/civilware/Gnomon/structures"
...

// API
api_endpoint := "127.0.0.1:8082"    // Listening non-ssl api endpoint
sslenabled := false // Enable/disable ssl listener
api_ssl_endpoint := "127.0.0.1:9092"    // API SSL endpoint
get_info_ssl_endpoint := "127.0.0.1:9394"   // Extra singular listener for get_info hosting on SSL
mbl := false    // asks api to host these queryable endpoints for mbl details
apic := &structures.APIConfig{
    Enabled:              true,
    Listen:               api_endpoint,
    StatsCollectInterval: "5s",
    SSL:                  sslenabled,
    SSLListen:            api_ssl_endpoint,
    GetInfoSSLListen:     get_info_ssl_endpoint,
    CertFile:             "fullchain.cer",  // Cert file for api ssl
    GetInfoCertFile:      "getinfofullchain.cer",   // Cert file for getinfo ssl
    KeyFile:              "cert.key",   // Key file for api ssl
    GetInfoKeyFile:       "getinfocert.key",    // Key file for getinfo ssl
    MBLLookup:            mbl,
}
```

## GnomonIndexer
The [gnomonindexer](/cmd/gnomonindexer/gnomonindexer.go) command line interface allows for kicking off an indexer to analyze the DERO blockchain transactions. This indexer is primarily used for indexing smart contract interactions and asset transactions.

You have the ability to index at a single block at a time to multiple blocks at a time. Keep in mind that the more you increase --num-parallel-blocks the greater the load on gnomonindexer as well as the daemon you're connected to. The number of parallel blocks is defaulted to 1.

```bash
./gnomonindexer --daemon-rpc-address=127.0.0.1:10102 --num-parallel-blocks=5
```

## GnomonSC Index Service
The [gnomonsc](/cmd/gnomonsc/gnomonsc.go) command line interface allows for setting up an index service which will index SCs based on an input search filter (or all if not defined) and store the SC height, owner and scid within the [contract](/cmd/gnomonsc/contracts/contract.bas). Today this is handled by a specific gnomon address which is more widely consumed throughout this package for things such as fastsync etc.

This configuration is pretty bruteforce in nature to define all of the SCID details that are relevant within another SC for lookup. Future states of this component may include a more chunked approach which could potentially be stored off-chain for consumption, however plans and roadmap for that is still a work in progress.

### Running the GnomonSC Index Service

```bash
# We are assuming that a daemon, wallet (with no username/pwd [TODO: not ideal and do not use anything outside of your same system]), and gnomon indexer are running
# Say we want to auto-index any SCs that match sending an asset in any form
./gnomonsc --daemon-rpc-address=127.0.0.1:10102 --wallet-rpc-address=127.0.0.1:10103 --gnomon-api-address=127.0.0.1:8082 --block-deploy-buffer=5 --search-filter="SEND_ASSET_TO_ADDRESS"
```