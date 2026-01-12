package wsserver

import (
	"context"
	"fmt"

	"github.com/civilware/Gnomon/indexer"
	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
)

// Manually add/inject a SCID to be indexed. Checks validity and then stores within owner tree (no signer addr) and stores a set of current variables.
// TODO: Could perform a getsc call in order to acquire owner and other misc info to pass along through the structures.FastSyncImport
func AddSCIDToIndex(ctx context.Context, p structures.WS_AddSCIDToIndex_Params, indexer *indexer.Indexer) (result structures.WS_AddSCIDToIndex_Result, err error) {
	logger = structures.Logger.WithFields(logrus.Fields{})

	scidstoadd := make(map[string]*structures.FastSyncImport)
	scidstoadd[p.SCID] = &structures.FastSyncImport{}
	err = indexer.AddSCIDToIndex(scidstoadd, false, true)
	if err != nil {
		logger.Printf("Err - %v", err)
		result.Result = fmt.Sprintf("Err - %v", err)
	} else {
		result.Result = fmt.Sprintf("Success")
	}

	return
}
