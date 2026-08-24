package cli

import (
	"flag"
	"fmt"

	"github.com/goradioserver/goradio/internal/config"
)

// Station implements
// `radio station [--config station.yaml] [--script station.lua] [args...]`.
//
// Any args left over after --config/--script are NOT treated as an error:
// they are passed straight through to the Lua script unparsed (exposed as
// radio.args), letting one shared script drive many different stations
// depending on how it's invoked.
func Station(args []string) error {
	fs := flag.NewFlagSet("station", flag.ContinueOnError)
	configPath := fs.String("config", "station.yaml", "path to the station config file")
	scriptPath := fs.String("script", "station.lua", "path to the Lua station script")
	// The stdlib flag package already stops parsing at the first non-flag
	// token, so --config/--script must precede any script passthrough args
	// (e.g. `radio station --config myfm.yaml myfm "My FM"`); everything
	// from that point on lands unparsed in fs.Args().
	if err := fs.Parse(args); err != nil {
		return err
	}
	scriptArgs := fs.Args()

	cfg, err := config.LoadStationConfig(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := newLogger(cfg.Logging.Level)
	log.Info("goradio station controller starting",
		"config", *configPath,
		"script", *scriptPath,
		"args", scriptArgs,
		"grpc_addr", cfg.Server.GRPCAddr,
	)

	return runStation(log, cfg, *scriptPath, scriptArgs)
}
