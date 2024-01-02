package indexer

import (
	"io"

	"github.com/civilware/Gnomon/structures"
	"github.com/sirupsen/logrus"
	prefixed "github.com/x-cray/logrus-prefixed-formatter"
)

func InitLog(args map[string]interface{}, console io.Writer) {
	loglevel_console := logrus.InfoLevel

	if args["--debug"] != nil && args["--debug"].(bool) == true {
		loglevel_console = logrus.DebugLevel
	}

	structures.Logger = logrus.Logger{
		Out:   console,
		Level: loglevel_console,
		Formatter: &prefixed.TextFormatter{
			ForceColors:     true,
			DisableColors:   false,
			TimestampFormat: "01/02/2006 15:04:05",
			FullTimestamp:   true,
			ForceFormatting: true,
		},
	}
}
