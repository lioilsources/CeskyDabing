package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/lioilsources/CeskyDabing/internal/config"
	"github.com/lioilsources/CeskyDabing/internal/workers/asr"
	"github.com/lioilsources/CeskyDabing/internal/workers/separate"
)

// doctorCmd checks the orchestrator host: shared storage, tools, DB, workers.
func (a *app) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Ověří úložiště (NFS), ffmpeg/rubberband, SQLite a dostupnost workerů",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			failed := 0
			check := func(name string, err error, ok string) {
				if err != nil {
					failed++
					fmt.Printf("✗ %-22s %v\n", name, err)
					return
				}
				fmt.Printf("✓ %-22s %s\n", name, ok)
			}

			fs, err := checkRoot(a.cfg.Root)
			check("úložiště "+a.cfg.Root, err, fs)
			for _, bin := range []string{a.cfg.FFmpeg, a.cfg.FFprobe, "rubberband"} {
				p, err := exec.LookPath(bin)
				check(bin, err, p)
			}
			st, err := a.openStore()
			if err == nil {
				st.Close()
			}
			check("sqlite", err, a.cfg.DB)

			hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			for _, w := range []struct {
				name   string
				cfg    config.Worker
				health func(url string) (string, error)
			}{
				{"separate", a.cfg.Workers.Separate, func(url string) (string, error) {
					c, err := separate.New(url, 5*time.Second)
					if err != nil {
						return "", err
					}
					h, err := c.Health(hctx)
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("backend=%s model=%s device=%s", h.Backend, h.Model, h.Device), nil
				}},
				{"asr", a.cfg.Workers.ASR, func(url string) (string, error) {
					c, err := asr.New(url, 5*time.Second)
					if err != nil {
						return "", err
					}
					h, err := c.Health(hctx)
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("backend=%s model=%s device=%s", h.Backend, h.Model, h.Device), nil
				}},
			} {
				msg, err := "", w.cfg.Validate(w.name)
				if err == nil {
					if msg, err = w.health(w.cfg.URL); err != nil {
						err = fmt.Errorf("%s: %w", w.cfg.URL, err)
					}
				}
				check("worker "+w.name, err, fmt.Sprintf("%s %s storage=%s", w.cfg.URL, msg, w.cfg.Storage))
			}
			if failed > 0 {
				return fmt.Errorf("%d kontrol selhalo", failed)
			}
			return nil
		},
	}
}

// checkRoot: the root exists, is writable and has the planned subdirectories.
func checkRoot(root string) (string, error) {
	if _, err := os.Stat(root); err != nil {
		return "", err
	}
	probe := filepath.Join(root, ".dub-doctor")
	if err := os.WriteFile(probe, []byte(time.Now().String()), 0o644); err != nil {
		return "", fmt.Errorf("nelze zapisovat: %w", err)
	}
	os.Remove(probe)
	var missing []string
	for _, d := range []string{"incoming", "work", "done", "voices", "series"} {
		if fi, err := os.Stat(filepath.Join(root, d)); err != nil || !fi.IsDir() {
			missing = append(missing, d)
		}
	}
	fs := fsType(root)
	msg := "fs=" + fs
	if fs != "" && fs != "nfs" {
		msg += " (pozor: není NFS — worker na Sparku stejné soubory neuvidí)"
	}
	if len(missing) > 0 {
		msg += fmt.Sprintf(", chybí adresáře %v", missing)
	}
	return msg, nil
}
