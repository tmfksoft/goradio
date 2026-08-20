package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// AudioServerConfig is the configuration for `radio serve`.
type AudioServerConfig struct {
	GRPC struct {
		ListenAddr string `yaml:"listen_addr"`
	} `yaml:"grpc"`

	HTTP struct {
		ListenAddr    string `yaml:"listen_addr"`
		PublicBaseURL string `yaml:"public_base_url"`
	} `yaml:"http"`

	Auth struct {
		JWTSecret string `yaml:"jwt_secret"`
	} `yaml:"auth"`

	Audio struct {
		AudioRoot string `yaml:"audio_root"`
	} `yaml:"audio"`

	Transcode struct {
		FfmpegPath     string `yaml:"ffmpeg_path"`
		CacheDir       string `yaml:"cache_dir"`
		BitrateKbps    int    `yaml:"bitrate_kbps"`
		SampleRate     int    `yaml:"sample_rate"`
		Channels       int    `yaml:"channels"`
		WorkerCount    int    `yaml:"worker_count"`
		TimeoutSeconds int    `yaml:"timeout_seconds"`
	} `yaml:"transcode"`

	Fetch struct {
		MaxDownloadBytes int64 `yaml:"max_download_bytes"`
	} `yaml:"fetch"`

	Silence struct {
		ClipDurationSeconds int `yaml:"clip_duration_seconds"`
	} `yaml:"silence"`

	Logging struct {
		Level string `yaml:"level"`
	} `yaml:"logging"`
}

// LoadAudioServerConfig reads and parses an AudioServerConfig from path,
// then applies defaults for any fields left unset.
func LoadAudioServerConfig(path string) (*AudioServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	cfg := &AudioServerConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	cfg.applyDefaults()

	if v := os.Getenv("GORADIO_JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}

	return cfg, nil
}

func (c *AudioServerConfig) applyDefaults() {
	if c.GRPC.ListenAddr == "" {
		c.GRPC.ListenAddr = "0.0.0.0:9090"
	}
	if c.HTTP.ListenAddr == "" {
		c.HTTP.ListenAddr = "0.0.0.0:8080"
	}
	if c.HTTP.PublicBaseURL == "" {
		c.HTTP.PublicBaseURL = "http://localhost:8080"
	}
	if c.Transcode.FfmpegPath == "" {
		c.Transcode.FfmpegPath = "ffmpeg"
	}
	if c.Transcode.CacheDir == "" {
		c.Transcode.CacheDir = "./data/transcode-cache"
	}
	if c.Transcode.BitrateKbps == 0 {
		c.Transcode.BitrateKbps = 128
	}
	if c.Transcode.SampleRate == 0 {
		c.Transcode.SampleRate = 44100
	}
	if c.Transcode.Channels == 0 {
		c.Transcode.Channels = 2
	}
	if c.Transcode.WorkerCount == 0 {
		c.Transcode.WorkerCount = 4
	}
	if c.Transcode.TimeoutSeconds == 0 {
		c.Transcode.TimeoutSeconds = 60
	}
	if c.Fetch.MaxDownloadBytes == 0 {
		c.Fetch.MaxDownloadBytes = 50 * 1024 * 1024
	}
	if c.Silence.ClipDurationSeconds == 0 {
		c.Silence.ClipDurationSeconds = 15
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
}
