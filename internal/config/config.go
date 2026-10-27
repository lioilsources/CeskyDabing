// Package config načítá dub.yaml. Pořadí: výchozí hodnoty < soubor < proměnné prostředí.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Root je kořen sdíleného úložiště (NFS z JODA), na M2 i Sparku stejná cesta.
	Root string `yaml:"root"`
	// DB je SQLite na lokálním disku M2.
	DB      string  `yaml:"db"`
	Workers Workers `yaml:"workers"`
	FFmpeg  string  `yaml:"ffmpeg"`
	FFprobe string  `yaml:"ffprobe"`
}

type Workers struct {
	Separate Worker `yaml:"separate"`
	ASR      Worker `yaml:"asr"`
	TTS      Worker `yaml:"tts"`
}

type Worker struct {
	URL string `yaml:"url"`
	// Storage: "shared" = worker vidí stejné NFS (předávají se cesty),
	// "upload" = fallback, soubory se nahrají na worker a výstupy stáhnou zpět.
	Storage string `yaml:"storage"`
	// RemoteRoot: kde worker vidí Root, pokud se cesty liší (výchozí = Root).
	RemoteRoot string        `yaml:"remote_root"`
	Timeout    time.Duration `yaml:"timeout"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Root: "/mnt/dabing",
		DB:   filepath.Join(home, ".local", "share", "dub", "dub.db"),
		Workers: Workers{
			Separate: Worker{URL: "http://spark:8101", Storage: "shared", Timeout: 30 * time.Minute},
			ASR:      Worker{URL: "http://spark:8102", Storage: "shared", Timeout: 30 * time.Minute},
			TTS:      Worker{URL: "http://spark:8103", Storage: "shared", Timeout: 5 * time.Minute},
		},
		FFmpeg:  "ffmpeg",
		FFprobe: "ffprobe",
	}
}

// DefaultPath je ~/.config/dub/dub.yaml (nebo $DUB_CONFIG).
func DefaultPath() string {
	if p := os.Getenv("DUB_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dub", "dub.yaml")
}

// Load načte soubor; chybějící soubor není chyba (použijí se výchozí hodnoty).
func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return cfg, err
	default:
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return cfg, fmt.Errorf("%s: %w", path, err)
		}
	}
	if v := os.Getenv("DUB_ROOT"); v != "" {
		cfg.Root = v
	}
	if v := os.Getenv("DUB_DB"); v != "" {
		cfg.DB = v
	}
	cfg.Root, cfg.DB = expandHome(cfg.Root), expandHome(cfg.DB)
	if v := os.Getenv("DUB_SEPARATE_URL"); v != "" {
		cfg.Workers.Separate.URL = v
	}
	if v := os.Getenv("DUB_SEPARATE_STORAGE"); v != "" {
		cfg.Workers.Separate.Storage = v
	}
	return cfg, nil
}

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// WorkDir je work/<serial>/<dil>/ pod kořenem.
func (c Config) WorkDir(series, episode string) string {
	return filepath.Join(c.Root, "work", series, episode)
}
