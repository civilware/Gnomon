### `addscid_toindex`

> Manually add/inject a SCID to be indexed. Checks validity and then stores within owner tree (no signer addr) and stores a set of current variables.

#### Params

|Name|Type|Required|Description|
|:--:|:--:|:------:|:---------:|
|scid|String|Mandatory|Supply SCID to add to index|
|skipfsrecheck|Boolean|Optional|Skipping search filter re-check|
|varstoreonly|Boolean|Optional|Regardless of SCID being indexed already or not in past, store the variables|

#### Request

```go
var pingpong structures.WS_AddSCIDToIndex_Result

params := structures.WS_AddSCIDToIndex_Params{
    SCID: "e12689bf2e670ab627c90a24cf6d1a3ad0f6eea80a0cc55c32a0af4bc77ce5d0",
    SkipFSRecheck: false,
    VarStoreOnly: true,
}

err = Client.RPC.CallResult(context.Background(), "addscid_toindex", params, &pingpong)
if err != nil {
    logger.Errorf("ERR - %v", err)
    Client.Connect("127.0.0.1:9190")
}

logger.Printf("[Return] %v", pingpong.Result)
```

#### Response

[Methods](../README.md#methods)