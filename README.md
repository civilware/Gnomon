![](https://github.com/civilware/gnomon/blob/main/gnomon.png?raw=true)

* Smart Contract querying and indexing
* Transaction querying and indexing
* Transaction Markup Language
* No custom daemon, no modified code.

# Using Gnomon As Software
Details coming soon.

# Using Gnomon As A Package
With Gnomon you have another choice, to use the repository as a package within whatever means you'd like to use it. The basis is to be able to leverage it for your own dApps or other configurations which may need to track specific contracts or data and use it appropriately.

## Search Filter(s)
```go
// Search filter - can be "" for any/everything, or specific to a piece of code in your template.bas files etc.
search_filter := "Function InitializePrivate() Uint64"
```

## Setting Up Database(s)
```go
import "github.com/civilware/Gnomon/storage"
...

// Database
var shasum string
shasum = fmt.Sprintf("%x", sha1.Sum([]byte("gnomon")))  // shasum can be used to unique a given db directory if you choose, can use normal words as well - whatever your choices.
db_folder := fmt.Sprintf("gnomondb\\%s_%s", "GNOMON", shasum)   // directory in current working directory. New gnomondb\GNOMON_shasum defined
Graviton_backend := storage.NewGravDB(db_folder, "25ms")
```

## Defining Your Indexer(s)
```go
import "github.com/civilware/Gnomon/indexer"
...

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

// Indexer
defaultIndexer := indexer.NewIndexer(Graviton_backend, search_filter, last_indexedheight, daemon_endpoint, runmode, mbl)
```

## Reading From Graviton DB(s)
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

// And so on... get functions within storage.go
```

## Defining API(s)
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