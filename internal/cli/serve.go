package cli

import (
	"flag"
	"fmt"

	"github.com/goradioserver/goradio/internal/config"
)

// Serve implements `radio serve [--config server.yaml]`.
func Serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", "server.yaml", "path to the audio server config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadAudioServerConfig(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := newLogger(cfg.Logging.Level)
	log.Info("goradio audio server starting",
		"config", *configPath,
		"grpc_addr", cfg.GRPC.ListenAddr,
		"http_addr", cfg.HTTP.ListenAddr,
	)

	return runServe(log, cfg)
}
