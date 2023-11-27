package structures

import (
	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
)

// Global logger
var Logger logrus.Logger

// Gnomon Index SCID
const MAINNET_GNOMON_SCID = "a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4"
const TESTNET_GNOMON_SCID = "c9d23d2fc3aaa8e54e238a2218c0e5176a6e48780920fd8474fac5b0576110a2"

// Max API data return for limiting data / load. This is unused if --remove-api-throttle is defined or ApiThrottle is false for structures.ApiConfig
const MAX_API_VAR_RETURN = 1024

// Major.Minor.Patch-Iteration
var Version = semver.MustParse("2.0.2-alpha.4")

// Hardcoded Smart Contracts of DERO Network
// TODO: Possibly in future we can pull this from derohe codebase
var Hardcoded_SCIDS = []string{"0000000000000000000000000000000000000000000000000000000000000001"}
