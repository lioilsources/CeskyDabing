package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfgPath := filepath.Join(dir, "dub.yaml")
	if err := os.WriteFile(cfgPath, []byte("root: /from/file\nworkers:\n  separate:\n    timeout: 5m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("# comment\nSPARK_HOST=gpu.example\nexport DUB_DB=\"~/x.db\"\nDUB_ROOT=/from/dotenv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DUB_ROOT", "/from/env") // real environment beats .env and the file
	t.Setenv("DUB_TTS_URL", "http://tts.example:9")
	t.Setenv("SPARK_HOST", "") // registered for cleanup, then unset below
	os.Unsetenv("SPARK_HOST")
	t.Setenv("DUB_DB", "")
	os.Unsetenv("DUB_DB")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	for _, c := range []struct{ got, want string }{
		{cfg.Root, "/from/env"},
		{cfg.DB, filepath.Join(home, "x.db")},
		{cfg.Workers.Separate.URL, "http://gpu.example:8101"},
		{cfg.Workers.ASR.URL, "http://gpu.example:8105"},
		{cfg.Workers.TTS.URL, "http://tts.example:9"},
		{cfg.Workers.Separate.Storage, "shared"},
		{cfg.LLM.URL, "http://gpu.example:8080/v1"},
		{cfg.LLM.Model, "openclaw-default"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if cfg.Workers.Separate.Timeout != 5*time.Minute {
		t.Errorf("timeout %v", cfg.Workers.Separate.Timeout)
	}
}

func TestWorkerValidate(t *testing.T) {
	if err := (Worker{}).Validate("separate"); err == nil {
		t.Fatal("worker without URL must fail validation")
	}
	if err := (Worker{URL: "http://x.example:1"}).Validate("separate"); err != nil {
		t.Fatal(err)
	}
}
