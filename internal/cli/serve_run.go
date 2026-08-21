package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
	"github.com/tmfksoft/goradio/internal/audiosource"
	"github.com/tmfksoft/goradio/internal/auth"
	"github.com/tmfksoft/goradio/internal/config"
	"github.com/tmfksoft/goradio/internal/grpcapi"
	"github.com/tmfksoft/goradio/internal/httpapi"
	"github.com/tmfksoft/goradio/internal/playback"
	"github.com/tmfksoft/goradio/internal/registry"
	"github.com/tmfksoft/goradio/internal/silence"
	"github.com/tmfksoft/goradio/internal/transcode"
)

// stationStarter implements grpcapi.StationStarter: it starts a station's
// player goroutine the first time that station is registered.
type stationStarter struct {
	log         *slog.Logger
	silencePath string
	playerCfg   playback.PlayerConfig
}

func (d *stationStarter) StartStation(st *playback.Station) {
	cfg := d.playerCfg
	cfg.SilencePath = d.silencePath
	go st.Run(context.Background(), d.log, cfg)
}

func runServe(log *slog.Logger, cfg *config.AudioServerConfig) error {
	if cfg.Auth.JWTSecret == "" || cfg.Auth.JWTSecret == "CHANGE_ME" {
		log.Warn("auth.jwt_secret is unset or the placeholder default; set a real secret before exposing this server")
	}

	params := transcode.EncodeParams{
		BitrateKbps: cfg.Transcode.BitrateKbps,
		SampleRate:  cfg.Transcode.SampleRate,
		Channels:    cfg.Transcode.Channels,
	}
	timeout := time.Duration(cfg.Transcode.TimeoutSeconds) * time.Second

	cache := transcode.NewCache(cfg.Transcode.CacheDir, cfg.Transcode.FfmpegPath, params, timeout)

	silenceDuration := time.Duration(cfg.Silence.ClipDurationSeconds) * time.Second
	silencePath, err := silence.EnsureClip(context.Background(), cfg.Transcode.FfmpegPath, cfg.Transcode.CacheDir, params, silenceDuration, timeout)
	if err != nil {
		return fmt.Errorf("generate silence clip: %w", err)
	}
	log.Info("silence clip ready", "path", silencePath)

	srcCfg := audiosource.Config{
		AudioRoot:        cfg.Audio.AudioRoot,
		MaxDownloadBytes: cfg.Fetch.MaxDownloadBytes,
		DownloadDir:      filepath.Join(cfg.Transcode.CacheDir, "tmp"),
	}
	pool := transcode.NewPool(log, cache, srcCfg, cfg.Transcode.WorkerCount)

	reg := registry.New()
	starter := &stationStarter{
		log:         log,
		silencePath: silencePath,
		playerCfg: playback.PlayerConfig{
			BitrateKbps: cfg.Transcode.BitrateKbps,
			SampleRate:  cfg.Transcode.SampleRate,
			Channels:    cfg.Transcode.Channels,
			FfmpegPath:  cfg.Transcode.FfmpegPath,
		},
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(auth.UnaryServerInterceptor([]byte(cfg.Auth.JWTSecret))),
		grpc.StreamInterceptor(auth.StreamServerInterceptor([]byte(cfg.Auth.JWTSecret))),
	)
	api := grpcapi.NewServer(log, reg, pool, starter, cfg.HTTP.PublicBaseURL)
	audioserverv1.RegisterAudioServerServiceServer(grpcServer, api)

	grpcLis, err := net.Listen("tcp", cfg.GRPC.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen grpc: %w", err)
	}

	mux := httpapi.NewMux(log, reg)
	httpServer := &http.Server{Addr: cfg.HTTP.ListenAddr, Handler: mux}

	errCh := make(chan error, 2)
	go func() {
		log.Info("grpc listening", "addr", cfg.GRPC.ListenAddr)
		errCh <- grpcServer.Serve(grpcLis)
	}()
	go func() {
		log.Info("http listening", "addr", cfg.HTTP.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		log.Info("shutting down", "signal", sig.String())
	}

	grpcServer.GracefulStop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)

	return nil
}
