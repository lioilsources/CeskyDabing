// Package config loads dub.yaml. Precedence: defaults < file < environment (.env included).
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Root is the shared storage root; M2 and SPARK must see it under the same path.
	Root string `yaml:"root"`
	// DB is SQLite on the local disk of M2, never on the shared storage.
	DB      string  `yaml:"db"`
	Workers Workers `yaml:"workers"`
	LLM     LLM     `yaml:"llm"`
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
	// Storage: "shared" passes paths on the shared storage, "upload" is the fallback
	// that uploads inputs to the worker and downloads outputs back.
	Storage string `yaml:"storage"`
	// RemoteRoot is where the worker sees Root when the paths differ (default Root).
	RemoteRoot string        `yaml:"remote_root"`
	Timeout    time.Duration `yaml:"timeout"`
}

// LLM is an OpenAI-compatible endpoint, by default the AiStack gateway on SPARK.
type LLM struct {
	URL     string        `yaml:"url"` // .../v1
	Model   string        `yaml:"model"`
	APIKey  string        `yaml:"-"` // only from DUB_LLM_KEY
	Timeout time.Duration `yaml:"timeout"`
}

// PortGateway of the AiStack LLM gateway on SPARK.
const PortGateway = 8080

// Default worker ports on SPARK. 8102–8104 are taken by other services there.
const (
	PortSeparate = 8101
	PortASR      = 8105
	PortTTS      = 8106
)

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Root: "/mnt/dabing",
		DB:   filepath.Join(home, ".local", "share", "dub", "dub.db"),
		Workers: Workers{
			Separate: Worker{Storage: "shared", Timeout: 30 * time.Minute},
			ASR:      Worker{Storage: "shared", Timeout: 30 * time.Minute},
			TTS:      Worker{Storage: "shared", Timeout: 5 * time.Minute},
		},
		LLM:     LLM{Model: "openclaw-default", Timeout: 5 * time.Minute},
		FFmpeg:  "ffmpeg",
		FFprobe: "ffprobe",
	}
}

// DefaultPath is $DUB_CONFIG or ~/.config/dub/dub.yaml.
func DefaultPath() string {
	if p := os.Getenv("DUB_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dub", "dub.yaml")
}

// Load reads the config file (a missing file is not an error) and applies the
// environment. Worker URLs left empty are derived from SPARK_HOST.
func Load(path string) (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
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
	setFromEnv(&cfg.Root, "DUB_ROOT")
	setFromEnv(&cfg.DB, "DUB_DB")
	cfg.Root, cfg.DB = expandHome(cfg.Root), expandHome(cfg.DB)

	host := os.Getenv("SPARK_HOST")
	for _, w := range []struct {
		w    *Worker
		name string
		port int
	}{
		{&cfg.Workers.Separate, "SEPARATE", PortSeparate},
		{&cfg.Workers.ASR, "ASR", PortASR},
		{&cfg.Workers.TTS, "TTS", PortTTS},
	} {
		setFromEnv(&w.w.URL, "DUB_"+w.name+"_URL")
		setFromEnv(&w.w.Storage, "DUB_"+w.name+"_STORAGE")
		if w.w.URL == "" && host != "" {
			w.w.URL = fmt.Sprintf("http://%s:%d", host, w.port)
		}
	}
	setFromEnv(&cfg.LLM.URL, "DUB_LLM_URL")
	setFromEnv(&cfg.LLM.Model, "DUB_LLM_MODEL")
	setFromEnv(&cfg.LLM.APIKey, "DUB_LLM_KEY")
	if cfg.LLM.URL == "" && host != "" {
		cfg.LLM.URL = fmt.Sprintf("http://%s:%d/v1", host, PortGateway)
	}
	return cfg, nil
}

// Validate reports a worker without an address; called only by commands that need it.
func (w Worker) Validate(name string) error {
	if w.URL == "" {
		return fmt.Errorf("worker %s nemá adresu: nastav SPARK_HOST v .env nebo DUB_%s_URL", name, strings.ToUpper(name))
	}
	return nil
}

func setFromEnv(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

// loadDotEnv sets KEY=VALUE lines from path without overriding the real environment.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
	return sc.Err()
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// WorkDir is work/<series>/<episode>/ under the root.
func (c Config) WorkDir(series, episode string) string {
	return filepath.Join(c.Root, "work", series, episode)
}
