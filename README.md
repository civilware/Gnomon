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
- [Documentation](#documentation)
- [GnomonIndexer (CLI)](#gnomonindexer-cli)
  - [CLI Help](#cli-help)
- [Using Gnomon As A Go Package](#using-gnomon-as-a-go-package)
  - [Search Filter(s)](#search-filters)
  - [Setting Up Database](#setting-up-database)
    - [BoltDB](#boltdb)
    - [GravitonDB](#gravitondb)
  - [Defining Your Indexer(s)](#defining-your-indexers)
  - [Reading From DB(s)](#reading-from-dbs)
    - [BoltDB](#boltdb-1)
    - [GravitonDB](#gravitondb-1)
  - [Defining API(s)](#defining-apis)
- [GnomonSC Index Service](#gnomonsc-index-service)
  - [Running the GnomonSC Index Service](#running-the-gnomonsc-index-service)

## Contributing
[Bug fixes](./.github/ISSUE_TEMPLATE/bug_report.md), [feature requests](./.github/ISSUE_TEMPLATE/feature_request.md) etc. can be submitted through normal issues on this repository. Feel free to follow the [Pull Request Template](./.github/pull_request_template.md) for any code merges.

## Documentation

[Documentation](./DOCS) including guides, [ROADMAP](./DOCS/ROADMAP.md), [CHANGELOG](./DOCS/CHANGELOG.md) and more.

## GnomonIndexer (CLI)
The [gnomonindexer](/cmd/gnomonindexer/gnomonindexer.go) command line interface allows for kicking off an indexer to analyze the DERO blockchain transactions. This indexer is primarily used for indexing smart contract interactions and asset transactions.

You have the ability to index at a single block at a time to multiple blocks at a time. Keep in mind that the more you increase --num-parallel-blocks the greater the load on gnomonindexer as well as the daemon you're connected to. The number of parallel blocks is defaulted to 1.

```bash
cd cmd/gnomonindexer
go build .
./gnomonindexer --daemon-rpc-address=127.0.0.1:10102 --num-parallel-blocks=5 --debug
```

### CLI Help
By typing ```help``` via the cli gnomonindexer you can print the below to utilize to interact with the index db.

```
commands:
	help		this help
	version		Show gnomon version
	listsc		Lists all indexed scids that match original search filter
	listsc_hardcoded		Lists all hardcoded scids
	listsc_code		Lists SCID code, listsc_code <scid>
	listsc_variables		Lists SCID variables at latest height unless optionally defining a height, listsc_variables <scid> <height>
	listsc_byowner	Lists SCIDs by owner, listsc_byowner <owneraddress>
	listsc_byscid	List a scid/owner pair by scid and optionally at a specified height and higher, listsc_byscid <scid> <minheight>
	listsc_byheight	List all indexed scids that match original search filter including height deployed, listsc_byheight
	listsc_balances	Lists balances of SCIDs that are greater than 0 or of a specific scid if specified, listsc_balances || listsc_balances <scid>
	listsc_byentrypoint	Lists sc invokes by entrypoint, listsc_byentrypoint <scid> <entrypoint>
	listsc_byinitialize	Lists all calls to SCs that attempted to run Initialize or InitializePrivate() or to a specific SC is defined, listsc_byinitialize || listsc_byinitialize <scid>
	listscinvoke_bysigner	Lists all sc invokes that match a given signer or partial signer address and optionally by scid, listscinvoke_bysigner <signerstring> || listscinvoke_bysigner <signerstring> <scid>
	listscidkey_byvaluestored	List keys in a SC that match a given value by pulling from gnomon database, listscidkey_byvaluestored <scid> <value>
	listscidkey_byvaluelive	List keys in a SC that match a given value by pulling from daemon, listscidkey_byvaluelive <scid> <value>
	listscidvalue_bykeystored	List keys in a SC that match a given value by pulling from gnomon database, listscidvalue_bykeystored <scid> <key>
	listscidvalue_bykeylive	List keys in a SC that match a given value by pulling from daemon, listscidvalue_bykeylive <scid> <key>
	validatesc	Validates a SC looking for a 'signature' k/v pair containing DERO signature validating the code matches the signature, validatesc <scid>
	addscid_toindex	Add a SCID to index list/validation filter manually, addscid_toindex <scid>
	getscidlist_byaddr	Gets list of scids that addr has interacted with, getscidlist_byaddr <addr>
	pop	Rolls back lastindexheight, pop <100>
	status		Show general information
	gnomonsc		Show scid of gnomon index scs
	bye		Quit the daemon
	exit		Quit the daemon
	quit		Quit the daemon
```

## Using Gnomon As A Go Package
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
if len(search_filter) == 0 {
  shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
} else {
  csearch_filter = strings.Join(search_filter, sf_separator)
  shasum = fmt.Sprintf("%x", sha1.Sum([]byte(csearch_filter)))
}

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
var shasum string
if len(search_filter) == 0 {
  shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))
} else {
  csearch_filter = strings.Join(search_filter, sf_separator)
  shasum = fmt.Sprintf("%x", sha1.Sum([]byte(csearch_filter)))
}
db_folder := fmt.Sprintf("gnomondb\\%s_%s", "GNOMON", shasum)
current_path, err := os.Getwd()
if err != nil {
  logger.Fatalf("[Main] Err getting working directory: %v", err)
}
db_path := filepath.Join(current_path, db_folder)
Graviton_backend := storage.NewGravDB(db_path, "25ms")
if err != nil {
  logger.Fatalf("[Main] Err creating gravdb: %v", err)
}
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

// RunMode - Daemon, Asset and Wallet. As of this release Daemon and Asset are the only ones doing any actions - wallet may be in the future.
// Daemon - Normal run operations indexing against the daemon.
// Asset - Lightweight operations indexing against the daemon which sole purpose is to grab initial data on hardcoded scids as well as a list of scids, owners, heights and code which are existing for reference.
runmode := "daemon"

// mbl - More of a 'bonus' feature - tracks/stores miniblock propogation on-chain as blocks are scanned. Boolean value to enable/disable this
// NOTE - This REQUIRES the same directory as your  DERODB etc. I'd suggest checking out binary implementation of this and adjust accordingly, most likely not used through packaging (but could be)
mbl := false

// closeondisconnect - More of a 'specific' use case feature - if daemon connectivity (after previously being connected) ceases for x time, then close the indexer which eventually panics. Will be cleaner in future or removed with upstream daemons being pooled. Primary use case is to ensure api is disconnected when daemon is not connected for bad data, could accomplish other ways.
closeondisconnect := false

// fastsync - Syncs against gnomon scid for scids and compares against search_filter, starts you at current topoheight
fastsync := false

// sfscidexclusion - A defined string array of SCIDs to exclude index operations on. This would primarily be used to skip over say the gnomonsc indexes (can also be leveraged with CLI gnomonindexer via --skip-gnomonsc-index) or any other specific SCIDs that may match a search_filter but you don't want to log their indexing operations.
var sfscidexclusion []string
sfscidexclusions = append(sfscidexclusion, structures.MAINNET_GNOMON_SCID)

// logrus logging init (optional)
indexer.InitLog(arguments, os.Stdout)

// Indexer
defaultIndexer := indexer.NewIndexer(Graviton_backend, Bbs_backend, dbtype, search_filter, last_indexedheight, daemon_endpoint, runmode, mbl, closeondisconnect, fastsync, sfscidexclusion)
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
last_indexedheight, err := Bbs_backend.GetLastIndexHeight()

// Get all SC transfer txs that are 'normal' transfers by a given address
allNormalTxWithSCID := Bbs_backend.GetAllNormalTxWithSCIDByAddr("address")

// Get all SCID invokes 
allSCIDInvokes := Bbs_backend.GetAllSCIDInvokeDetails("scid")

// And so on... get functions within bbolt.go
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
last_indexedheight, _ := Graviton_backend.GetLastIndexHeight()

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
api_throttle = false  // If enabled, throttle responses from API to a limit defined by structures.MAX_API_VAR_RETURN . This may be valuable for public-facing query nodes.
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
    ApiThrottle:          api_throttle,
}
```

## GnomonSC Index Service
The [gnomonsc](/cmd/gnomonsc/gnomonsc.go) command line interface allows for setting up an index service which will index SCs based on an input search filter (or all if not defined) and store the SC height, owner and scid within the [contract](/cmd/gnomonsc/contracts/contract.bas). Today this is handled by a specific gnomon address which is more widely consumed throughout this package for things such as fastsync etc.

This configuration is pretty bruteforce in nature to define all of the SCID details that are relevant within another SC for lookup. Future states of this component may include a more chunked approach which could potentially be stored off-chain for consumption, however plans and roadmap for that is still a work in progress.

### Running the GnomonSC Index Service

```bash
cd cmd/gnomonsc
go build .
# We are assuming that a daemon, wallet (with no username/pwd [TODO: not ideal and do not use anything outside of your same system]), and gnomon indexer are running
# Say we want to auto-index any SCs that match sending an asset in any form
./gnomonsc --daemon-rpc-address=127.0.0.1:10102 --wallet-rpc-address=127.0.0.1:10103 --gnomon-api-address=127.0.0.1:8082 --block-deploy-buffer=5 --search-filter="SEND_ASSET_TO_ADDRESS"
```