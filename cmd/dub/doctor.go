package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/lioilsources/CeskyDabing/internal/workers/separate"
)

// doctorCmd ověří prostředí M2: sdílené úložiště, nástroje, DB, workery.
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
			wc := a.cfg.Workers.Separate
			w, err := separate.New(wc.URL, 5*time.Second)
			if err == nil {
				var h *separate.Health
				if h, err = w.Health(hctx); err == nil {
					check("worker separate", nil, fmt.Sprintf("%s %s backend=%s model=%s device=%s storage=%s",
						h.Status, wc.URL, h.Backend, h.Model, h.Device, wc.Storage))
				}
			}
			if err != nil {
				check("worker separate", fmt.Errorf("%s: %w", wc.URL, err), "")
			}
			if failed > 0 {
				return fmt.Errorf("%d kontrol selhalo", failed)
			}
			return nil
		},
	}
}

// checkRoot: kořen existuje, je zapisovatelný a má adresáře z plánu.
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
