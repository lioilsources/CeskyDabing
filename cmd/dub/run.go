package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lioilsources/CeskyDabing/internal/config"
	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/pipeline"
	"github.com/lioilsources/CeskyDabing/internal/storage"
	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/translate"
	"github.com/lioilsources/CeskyDabing/internal/tts"
	"github.com/lioilsources/CeskyDabing/internal/workers/asr"
	"github.com/lioilsources/CeskyDabing/internal/workers/separate"
)

func (a *app) runCmd() *cobra.Command {
	var opt pipeline.Options
	var so stepOptions
	var metricsJSON bool
	var m runMetrics
	var failures []error
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
			runner, err := a.runner(st, so)
			if err != nil {
				return err
			}
			for _, ep := range eps {
				r := *runner
				r.Log = a.log.With("ep", ep.Series+"/"+ep.Slug)
				res, err := r.Run(ctx, ep, opt)
				m.Steps.Ran += len(res.Ran)
				m.Steps.Skipped += len(res.Skipped)
				if err != nil {
					m.Episodes.Failed++
					failures = append(failures, fmt.Errorf("%s/%s: %w", ep.Series, ep.Slug, err))
					if ctx.Err() != nil {
						break // preempted: do not start further episodes
					}
					continue
				}
				m.Episodes.OK++
			}
			if metricsJSON {
				// sparkctl done/exec reads the last output line as JSON metrics.
				b, _ := json.Marshal(m)
				fmt.Println(string(b))
			}
			return errors.Join(failures...)
		},
	}
	cmd.Flags().BoolVar(&metricsJSON, "metrics-json", false, "na konci vypsat JSON metriky (poslední řádek pro sparkctl)")
	cmd.Flags().StringVar(&opt.From, "from", "", "spustit znovu od kroku")
	cmd.Flags().StringVar(&opt.Until, "until", "", "skončit po kroku")
	cmd.Flags().BoolVar(&opt.Force, "force", false, "spustit znovu i hotové kroky")
	cmd.Flags().StringVar(&so.separateModel, "separate-model", "", "Demucs model (výchozí dle workeru, htdemucs_ft)")
	cmd.Flags().BoolVar(&so.keepStems, "keep-stems", true, "uložit i drums/bass/other (detekce písniček)")
	cmd.Flags().StringVar(&so.language, "language", "en", "jazyk originálu (ISO 639-1)")
	return cmd
}

// stepOptions are the per-step flags of dub run.
type stepOptions struct {
	separateModel string
	keepStems     bool
	language      string
}

func (a *app) runner(st *store.Store, so stepOptions) (*pipeline.Runner, error) {
	ff := media.FFmpeg{FFmpeg: a.cfg.FFmpeg, FFprobe: a.cfg.FFprobe}
	steps := map[string]pipeline.Step{"extract": pipeline.Extract{FF: ff}}
	newStorage := func(wc config.Worker, t storage.Transport) func() (storage.Storage, error) {
		return func() (storage.Storage, error) { return storage.New(wc.Storage, a.cfg.Root, wc.RemoteRoot, t) }
	}

	// A step whose worker is not configured fails only when it is reached, so
	// earlier steps keep working without it.
	if wc := a.cfg.Workers.Separate; wc.Validate("separate") != nil {
		steps["separate"] = failing{wc.Validate("separate")}
	} else {
		w, err := separate.New(wc.URL, wc.Timeout)
		if err != nil {
			return nil, err
		}
		steps["separate"] = pipeline.Separate{
			Worker: w, Store: st, Model: so.separateModel, KeepStems: so.keepStems, NewStorage: newStorage(wc, w),
		}
	}
	if wc := a.cfg.Workers.ASR; wc.Validate("asr") != nil {
		steps["transcribe"] = failing{wc.Validate("asr")}
	} else {
		w, err := asr.New(wc.URL, wc.Timeout)
		if err != nil {
			return nil, err
		}
		steps["transcribe"] = pipeline.Transcribe{Worker: w, Store: st, Language: so.language, NewStorage: newStorage(wc, w)}
	}
	steps["identify"] = pipeline.Identify{Store: st, Root: a.cfg.Root, Log: a.log}
	steps["refs"] = pipeline.Refs{Store: st, Root: a.cfg.Root, Log: a.log}
	if l := a.cfg.LLM; l.URL == "" {
		steps["translate"] = failing{fmt.Errorf("LLM nemá adresu: nastav SPARK_HOST v .env nebo DUB_LLM_URL")}
	} else {
		steps["translate"] = pipeline.Translate{Store: st, Root: a.cfg.Root, LLM: translate.NewHTTPLLM(l.URL, l.Model, l.APIKey, l.Timeout)}
	}
	if t := a.cfg.TTS; t.URL == "" {
		steps["synthesize"] = failing{fmt.Errorf("TTS nemá adresu: nastav SPARK_HOST v .env nebo DUB_TTS_URL")}
		steps["fit"] = steps["synthesize"]
	} else {
		synth := pipeline.Synthesize{
			Store: st, Root: a.cfg.Root, FF: ff, Parallel: t.Parallel, Log: a.log,
			TTS: tts.NewAiStack(t.URL, t.APIKey, t.VoiceRights, t.VoiceSource, t.Timeout),
		}
		steps["synthesize"] = synth
		steps["fit"] = pipeline.Fit{
			Synth: synth, LLM: translate.NewHTTPLLM(a.cfg.LLM.URL, a.cfg.LLM.Model, a.cfg.LLM.APIKey, a.cfg.LLM.Timeout),
			Stretch: pipeline.Rubberband{FFmpeg: a.cfg.FFmpeg, Rubberband: a.cfg.Rubberband}, Log: a.log,
		}
	}
	steps["mix"] = pipeline.Mix{Store: st, FF: ff, Log: a.log}
	steps["mux"] = pipeline.Mux{Store: st, FF: ff, Root: a.cfg.Root}
	return &pipeline.Runner{Store: st, Log: a.log, Steps: steps}, nil
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

type runMetrics struct {
	Episodes struct {
		OK     int `json:"ok"`
		Failed int `json:"failed"`
	} `json:"episodes"`
	Steps struct {
		Ran     int `json:"ran"`
		Skipped int `json:"skipped"`
	} `json:"steps"`
}

// failing is a step that cannot run because its configuration is missing.
type failing struct{ err error }

func (f failing) Run(context.Context, *pipeline.Episode) (pipeline.Outputs, error) { return nil, f.err }
