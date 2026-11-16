// Package pipeline is the state machine of dubbing steps for one episode.
//
// Each step writes into work/<series>/<episode>/ under fixed names and its state
// lives in episode_steps. A done step whose outputs exist on disk is skipped.
// When a step runs again, every later step is invalidated.
package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
)

// Steps is the step order (00-DABING_PLAN §2).
var Steps = []string{
	"extract", "separate", "transcribe", "identify", "refs",
	"translate", "synthesize", "fit", "mix", "mux",
}

var ErrNotImplemented = errors.New("krok zatím není implementovaný")

// Outputs maps an output name to a path relative to the episode work dir.
type Outputs map[string]string

type Episode struct {
	db.Episode
	Series string
}

// Step is one pipeline step. Run must be idempotent and write outputs
// atomically (temp file + rename).
type Step interface {
	Run(ctx context.Context, ep *Episode) (Outputs, error)
}

type Runner struct {
	Store *store.Store
	Steps map[string]Step
	Log   *slog.Logger
}

type Options struct {
	From  string // rerun from this step (earlier steps must be done)
	Until string // stop after this step (inclusive)
	Force bool   // rerun done steps in range too
}

// Validate checks step names and their order.
func (o Options) Validate() error {
	_, _, err := o.bounds()
	return err
}

func (o Options) bounds() (from, until int, err error) {
	from, until = 0, len(Steps)-1
	if o.From != "" {
		if from = slices.Index(Steps, o.From); from < 0 {
			return 0, 0, fmt.Errorf("neznámý krok %q", o.From)
		}
	}
	if o.Until != "" {
		if until = slices.Index(Steps, o.Until); until < 0 {
			return 0, 0, fmt.Errorf("neznámý krok %q", o.Until)
		}
	}
	if from > until {
		return 0, 0, fmt.Errorf("--from %s je až za --until %s", o.From, o.Until)
	}
	return from, until, nil
}

func (r *Runner) Run(ctx context.Context, ep *Episode, opt Options) error {
	from, until, err := opt.bounds()
	if err != nil {
		return err
	}
	states, err := r.states(ctx, ep.ID)
	if err != nil {
		return err
	}
	for i := 0; i <= until; i++ {
		name := Steps[i]
		st, ok := states[name]
		fresh := ok && st.Status == "done" && r.outputsExist(ep, st)
		if i < from {
			if !fresh {
				return fmt.Errorf("--from %s: předchozí krok %s není hotový", opt.From, name)
			}
			continue
		}
		if fresh && !opt.Force && opt.From != name {
			r.Log.Info("přeskakuji, hotovo", "step", name)
			continue
		}
		step := r.Steps[name]
		if step == nil {
			return fmt.Errorf("%s: %w", name, ErrNotImplemented)
		}
		if err := r.runStep(ctx, ep, name, step); err != nil {
			return err
		}
		// A step that ran invalidates every later step.
		for _, later := range Steps[i+1:] {
			if err := r.Store.ResetStep(ctx, db.ResetStepParams{EpisodeID: ep.ID, Step: later}); err != nil {
				return err
			}
			delete(states, later)
		}
	}
	return nil
}

func (r *Runner) runStep(ctx context.Context, ep *Episode, name string, step Step) error {
	if err := os.MkdirAll(ep.WorkDir, 0o755); err != nil {
		return err
	}
	if err := r.Store.StartStep(ctx, db.StartStepParams{EpisodeID: ep.ID, Step: name}); err != nil {
		return err
	}
	r.Log.Info("start", "step", name)
	t0 := time.Now()
	outs, err := step.Run(ctx, ep)
	if err != nil {
		// Recording the failure must survive a cancelled context.
		_ = r.Store.FailStep(context.WithoutCancel(ctx), db.FailStepParams{Error: err.Error(), EpisodeID: ep.ID, Step: name})
		return fmt.Errorf("%s: %w", name, err)
	}
	b, _ := json.Marshal(outs)
	if err := r.Store.FinishStep(ctx, db.FinishStepParams{Outputs: string(b), EpisodeID: ep.ID, Step: name}); err != nil {
		return err
	}
	r.Log.Info("hotovo", "step", name, "trvání", time.Since(t0).Round(time.Millisecond))
	return nil
}

func (r *Runner) states(ctx context.Context, episodeID int64) (map[string]db.EpisodeStep, error) {
	rows, err := r.Store.ListSteps(ctx, episodeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	m := make(map[string]db.EpisodeStep, len(rows))
	for _, s := range rows {
		m[s.Step] = s
	}
	return m, nil
}

func (r *Runner) outputsExist(ep *Episode, st db.EpisodeStep) bool {
	var outs Outputs
	if err := json.Unmarshal([]byte(st.Outputs), &outs); err != nil {
		return false
	}
	for _, rel := range outs {
		if _, err := os.Stat(filepath.Join(ep.WorkDir, rel)); err != nil {
			r.Log.Warn("výstup chybí, krok poběží znovu", "step", st.Step, "file", rel)
			return false
		}
	}
	return true
}

// Status returns all steps in order; missing ones are pending.
func (r *Runner) Status(ctx context.Context, ep *Episode) ([]db.EpisodeStep, error) {
	states, err := r.states(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	out := make([]db.EpisodeStep, 0, len(Steps))
	for _, name := range Steps {
		st, ok := states[name]
		if !ok {
			st = db.EpisodeStep{EpisodeID: ep.ID, Step: name, Status: "pending"}
		}
		out = append(out, st)
	}
	return out, nil
}
