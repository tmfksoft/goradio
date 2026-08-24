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

	"connectrpc.com/connect"

	"github.com/goradioserver/goradio/gen/go/audioserver/v1/audioserverv1connect"
	"github.com/goradioserver/goradio/internal/audiosource"
	"github.com/goradioserver/goradio/internal/auth"
	"github.com/goradioserver/goradio/internal/config"
	"github.com/goradioserver/goradio/internal/grpcapi"
	"github.com/goradioserver/goradio/internal/httpapi"
	"github.com/goradioserver/goradio/internal/playback"
	"github.com/goradioserver/goradio/internal/registry"
	"github.com/goradioserver/goradio/internal/silence"
	"github.com/goradioserver/goradio/internal/transcode"
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
	ctx, cancel := context.WithCancel(context.Background())
	st.SetRunCancel(cancel)
	go st.Run(ctx, d.log, cfg)
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

	api := grpcapi.NewServer(log, reg, pool, starter, cfg.HTTP.PublicBaseURL)
	apiPath, apiHandler := audioserverv1connect.NewAudioServerServiceHandler(
		api,
		connect.WithInterceptors(auth.NewInterceptor([]byte(cfg.Auth.JWTSecret))),
	)
	apiMux := http.NewServeMux()
	apiMux.Handle(apiPath, apiHandler)

	apiServer := &http.Server{
		Handler: apiMux,
		// A connect-go handler serves gRPC, gRPC-Web and the Connect
		// protocol off this one mux. gRPC requires HTTP/2, and this
		// listener is plaintext (TLS, when used, is terminated by a
		// reverse proxy in front), so UnencryptedHTTP2 -- h2c -- has to be
		// enabled explicitly or a grpc-go client can't negotiate at all.
		// HTTP1 stays on for Connect-protocol clients that only speak
		// HTTP/1.1, which is the whole point of exposing Connect: a caller
		// needs nothing but an HTTP client and a JSON parser.
		Protocols: httpProtocols(),
		HTTP2: &http.HTTP2Config{
			// The net/http equivalents of the grpc.KeepaliveParams this
			// replaced: probe a connection that's gone quiet, and drop it
			// if the probe goes unanswered, so a silently-dead client
			// (NAT/LB idle timeout, network partition) doesn't hold its
			// goroutine and event subscription open forever.
			SendPingTimeout: 30 * time.Second,
			PingTimeout:     10 * time.Second,
		},
	}

	apiLis, err := net.Listen("tcp", cfg.GRPC.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen grpc: %w", err)
	}

	mux := httpapi.NewMux(log, reg, []byte(cfg.Auth.JWTSecret))
	httpServer := &http.Server{Addr: cfg.HTTP.ListenAddr, Handler: mux}

	errCh := make(chan error, 2)
	go func() {
		log.Info("rpc listening (grpc, grpc-web, connect)", "addr", cfg.GRPC.ListenAddr)
		if err := apiServer.Serve(apiLis); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = apiServer.Shutdown(shutdownCtx)
	_ = httpServer.Shutdown(shutdownCtx)

	return nil
}

// httpProtocols is the protocol set for the RPC listener: HTTP/1.1 for
// Connect-protocol clients, plus h2c so grpc-go clients can negotiate
// HTTP/2 over a plaintext connection.
func httpProtocols() *http.Protocols {
	var p http.Protocols
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	return &p
}
