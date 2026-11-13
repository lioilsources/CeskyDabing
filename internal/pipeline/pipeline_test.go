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
	if _, err := r.Run(ctx, ep, Options{Until: "transcribe"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, ep, Options{Until: "transcribe"}); err != nil {
		t.Fatal(err)
	}
	if got := len(*runs); got != 3 {
		t.Fatalf("druhý běh neměl nic spouštět, běhy: %v", *runs)
	}

	// A deleted output reruns that step and every later one.
	os.Remove(filepath.Join(ep.WorkDir, "separate.txt"))
	*runs = nil
	if _, err := r.Run(ctx, ep, Options{Until: "transcribe"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"separate", "transcribe"}; !equal(*runs, want) {
		t.Fatalf("běhy %v, chci %v", *runs, want)
	}

	// --from forces the step and the later ones.
	*runs = nil
	if _, err := r.Run(ctx, ep, Options{From: "extract", Until: "separate"}); err != nil {
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
	_, err := r.Run(context.Background(), ep, Options{From: "separate", Until: "separate"})
	if err == nil {
		t.Fatal("čekal jsem chybu: extract není hotový")
	}
}

func TestRunStopsOnUnimplementedAndFailure(t *testing.T) {
	r, ep, runs := setup(t)
	ctx := context.Background()
	if _, err := r.Run(ctx, ep, Options{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("chci ErrNotImplemented, mám %v", err)
	}
	r.Steps["separate"] = fakeStep{name: "separate", runs: runs, err: errors.New("bum")}
	if _, err := r.Run(ctx, ep, Options{From: "separate", Until: "separate"}); err == nil {
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

type blockingStep struct{ started chan struct{} }

func (b blockingStep) Run(ctx context.Context, _ *Episode) (Outputs, error) {
	close(b.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

// sparkctl exec stops a preempted command with SIGTERM; the step must end up
// failed and run again on the next start.
func TestCancelledStepRunsAgain(t *testing.T) {
	r, ep, runs := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	r.Steps["extract"] = blockingStep{started: started}
	done := make(chan error)
	go func() {
		_, err := r.Run(ctx, ep, Options{Until: "extract"})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	status, _ := r.Status(context.Background(), ep)
	if status[0].Status != "failed" {
		t.Fatalf("extract: %+v", status[0])
	}

	r.Steps["extract"] = fakeStep{name: "extract", runs: runs}
	res, err := r.Run(context.Background(), ep, Options{Until: "extract"})
	if err != nil || !equal(res.Ran, []string{"extract"}) {
		t.Fatalf("rerun: %+v, %v", res, err)
	}
}

// A process killed with SIGKILL leaves the step "running"; that is not done.
func TestStaleRunningStepRunsAgain(t *testing.T) {
	r, ep, runs := setup(t)
	ctx := context.Background()
	if err := r.Store.StartStep(ctx, db.StartStepParams{EpisodeID: ep.ID, Step: "extract"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, ep, Options{Until: "extract"}); err != nil {
		t.Fatal(err)
	}
	if !equal(*runs, []string{"extract"}) {
		t.Fatalf("runs %v", *runs)
	}
}
