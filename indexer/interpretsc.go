package indexer

import (
	"time"

	"github.com/deroproject/derohe/dvm"
)

func (indexer *Indexer) InterpretSC(scid string, code string) {

	return

	SC, pos, err := dvm.ParseSmartContract(code)
	if err != nil {
		logger.Errorf("[InterpretSC] ERR on SC Parse SCID '%s' - %v", scid, err)
		return
	}

	logger.Debugf("[InterpretSC] SCID '%s' - pos: %v ; SC: %v", scid, pos, SC)

	params := make(map[string]interface{})
	entrypoint := "InputSCID"
	state := &dvm.Shared_State{Chain_inputs: &dvm.Blockchain_Input{}}
	result, err := dvm.RunSmartContract(&SC, entrypoint, state, params)

	if err != nil {
		logger.Errorf("[InterpretSC] ERR on RunSmartContract SCID '%s' - %v", scid, err)
		time.Sleep(60 * time.Second)
		return
	}

	logger.Debugf("[InterpetSC] SCID '%s' - entrypoint '%s' - result: %v", scid, entrypoint, result)
	time.Sleep(60 * time.Second)
}
