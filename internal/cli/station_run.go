package cli

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/goradioserver/goradio/internal/config"
	"github.com/goradioserver/goradio/internal/luastation"
)

func runStation(log *slog.Logger, cfg *config.StationConfig, scriptPath string, scriptArgs []string) error {
	if cfg.Auth.JWT == "" {
		log.Warn("auth.jwt is unset; RegisterStation will be rejected by the audio server")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	engine := luastation.NewEngine(log, cfg, scriptPath, scriptArgs)
	return engine.Run(ctx)
}
