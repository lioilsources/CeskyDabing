package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/pipeline"
	"github.com/lioilsources/CeskyDabing/internal/storage"
	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/workers/separate"
)

func (a *app) runCmd() *cobra.Command {
	var opt pipeline.Options
	var model string
	var keepStems bool
	cmd := &cobra.Command{
		Use:   "run <serial> [díl...]",
		Short: "Spustí pipeline (hotové kroky přeskočí); bez dílů = všechny díly seriálu",
		Long:  "Kroky: " + strings.Join(pipeline.Steps, " → "),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opt.Validate(); err != nil {
				return err
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			eps, err := a.episodes(ctx, st, args[0], args[1:])
			if err != nil {
				return err
			}
			runner, err := a.runner(st, model, keepStems)
			if err != nil {
				return err
			}
			for _, ep := range eps {
				r := *runner
				r.Log = a.log.With("ep", ep.Series+"/"+ep.Slug)
				if err := r.Run(ctx, ep, opt); err != nil {
					return fmt.Errorf("%s/%s: %w", ep.Series, ep.Slug, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opt.From, "from", "", "spustit znovu od kroku")
	cmd.Flags().StringVar(&opt.Until, "until", "", "skončit po kroku")
	cmd.Flags().BoolVar(&opt.Force, "force", false, "spustit znovu i hotové kroky")
	cmd.Flags().StringVar(&model, "separate-model", "", "Demucs model (výchozí dle workeru, htdemucs_ft)")
	cmd.Flags().BoolVar(&keepStems, "keep-stems", true, "uložit i drums/bass/other (detekce písniček)")
	return cmd
}

func (a *app) runner(st *store.Store, model string, keepStems bool) (*pipeline.Runner, error) {
	ff := media.FFmpeg{FFmpeg: a.cfg.FFmpeg, FFprobe: a.cfg.FFprobe}
	wc := a.cfg.Workers.Separate
	sep, err := separate.New(wc.URL, wc.Timeout)
	if err != nil {
		return nil, err
	}
	return &pipeline.Runner{
		Store: st,
		Log:   a.log,
		Steps: map[string]pipeline.Step{
			"extract": pipeline.Extract{FF: ff},
			"separate": pipeline.Separate{
				Worker: sep, Store: st, Model: model, KeepStems: keepStems,
				NewStorage: func() (storage.Storage, error) {
					return storage.New(wc.Storage, a.cfg.Root, wc.RemoteRoot, sep)
				},
			},
		},
	}, nil
}

func (a *app) episodes(ctx context.Context, st *store.Store, series string, slugs []string) ([]*pipeline.Episode, error) {
	s, err := st.GetSeries(ctx, slugify(series))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("seriál %q neexistuje (dub add)", series)
	} else if err != nil {
		return nil, err
	}
	var rows []db.Episode
	if len(slugs) == 0 {
		if rows, err = st.ListEpisodes(ctx, s.ID); err != nil {
			return nil, err
		}
	}
	for _, slug := range slugs {
		ep, err := st.GetEpisode(ctx, db.GetEpisodeParams{SeriesID: s.ID, Slug: slug})
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("díl %s/%s neexistuje", s.Slug, slug)
		} else if err != nil {
			return nil, err
		}
		rows = append(rows, ep)
	}
	out := make([]*pipeline.Episode, len(rows))
	for i, r := range rows {
		out[i] = &pipeline.Episode{Episode: r, Series: s.Slug}
	}
	return out, nil
}
