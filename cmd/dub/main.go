// Command dub je orchestrátor českého AI dabingu (DABING_PLAN.md).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/lioilsources/CeskyDabing/internal/config"
	"github.com/lioilsources/CeskyDabing/internal/store"
)

type app struct {
	cfgPath string
	cfg     config.Config
	log     *slog.Logger
}

func (a *app) openStore() (*store.Store, error) {
	return store.Open(a.cfg.DB)
}

func main() {
	a := &app{log: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	root := &cobra.Command{
		Use:           "dub",
		Short:         "Český AI dabing dětských seriálů EN → CS",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			cfg, err := config.Load(a.cfgPath)
			a.cfg = cfg
			return err
		},
	}
	root.PersistentFlags().StringVar(&a.cfgPath, "config", config.DefaultPath(), "konfigurační soubor")
	root.AddCommand(a.addCmd(), a.runCmd(), a.statusCmd(), a.doctorCmd())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "chyba:", err)
		os.Exit(1)
	}
}
