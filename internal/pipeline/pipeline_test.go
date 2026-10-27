package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/lioilsources/CeskyDabing/internal/store"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
)

type fakeStep struct {
	name string
	runs *[]string
	err  error
}

func (f fakeStep) Run(_ context.Context, ep *Episode) (Outputs, error) {
	*f.runs = append(*f.runs, f.name)
	if f.err != nil {
		return nil, f.err
	}
	return Outputs{"out": f.name + ".txt"}, os.WriteFile(filepath.Join(ep.WorkDir, f.name+".txt"), nil, 0o644)
}

func setup(t *testing.T) (*Runner, *Episode, *[]string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "dub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	s, err := st.UpsertSeries(ctx, db.UpsertSeriesParams{Slug: "s", Title: "S"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := st.CreateEpisode(ctx, db.CreateEpisodeParams{SeriesID: s.ID, Slug: "e1", InputPath: "in.mkv", WorkDir: filepath.Join(dir, "work")})
	if err != nil {
		t.Fatal(err)
	}
	runs := &[]string{}
	r := &Runner{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Steps: map[string]Step{}}
	for _, n := range Steps[:3] {
		r.Steps[n] = fakeStep{name: n, runs: runs}
	}
	return r, &Episode{Episode: e, Series: "s"}, runs
}

func TestRunSkipsDoneAndInvalidatesLater(t *testing.T) {
	r, ep, runs := setup(t)
	ctx := context.Background()
	if err := r.Run(ctx, ep, Options{Until: "transcribe"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx, ep, Options{Until: "transcribe"}); err != nil {
		t.Fatal(err)
	}
	if got := len(*runs); got != 3 {
		t.Fatalf("druhý běh neměl nic spouštět, běhy: %v", *runs)
	}

	// Smazaný výstup → krok znovu a všechny pozdější s ním.
	os.Remove(filepath.Join(ep.WorkDir, "separate.txt"))
	*runs = nil
	if err := r.Run(ctx, ep, Options{Until: "transcribe"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"separate", "transcribe"}; !equal(*runs, want) {
		t.Fatalf("běhy %v, chci %v", *runs, want)
	}

	// --from vynutí krok a pozdější.
	*runs = nil
	if err := r.Run(ctx, ep, Options{From: "extract", Until: "separate"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"extract", "separate"}; !equal(*runs, want) {
		t.Fatalf("běhy %v, chci %v", *runs, want)
	}
	status, _ := r.Status(ctx, ep)
	if status[2].Status != "pending" {
		t.Fatalf("transcribe měl být zneplatněný, je %s", status[2].Status)
	}
}

func TestRunFromRequiresEarlierDone(t *testing.T) {
	r, ep, _ := setup(t)
	err := r.Run(context.Background(), ep, Options{From: "separate", Until: "separate"})
	if err == nil {
		t.Fatal("čekal jsem chybu: extract není hotový")
	}
}

func TestRunStopsOnUnimplementedAndFailure(t *testing.T) {
	r, ep, runs := setup(t)
	ctx := context.Background()
	if err := r.Run(ctx, ep, Options{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("chci ErrNotImplemented, mám %v", err)
	}
	r.Steps["separate"] = fakeStep{name: "separate", runs: runs, err: errors.New("bum")}
	if err := r.Run(ctx, ep, Options{From: "separate", Until: "separate"}); err == nil {
		t.Fatal("chci chybu kroku")
	}
	status, _ := r.Status(ctx, ep)
	if status[1].Status != "failed" || status[1].Error != "bum" {
		t.Fatalf("separate: %+v", status[1])
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
