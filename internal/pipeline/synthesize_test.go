package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/lioilsources/CeskyDabing/internal/media"
	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/tts"
)

type fakeTTS struct {
	calls atomic.Int32
	dir   string
}

func (f *fakeTTS) EnsureVoice(_ context.Context, v tts.Voice) (string, error) {
	return "voice-" + v.Character, nil
}

// Synthesize returns a WAV of 0.1 s per rune.
func (f *fakeTTS) Synthesize(_ context.Context, r tts.Request) ([]byte, error) {
	f.calls.Add(1)
	a := &media.Audio{Rate: 1000, Channels: 1, Samples: make([]float32, 100*len([]rune(r.Text)))}
	dir, err := os.MkdirTemp(f.dir, "call") // calls run in parallel
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "x.wav")
	if err := media.WriteWAV(p, a); err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func TestSynthesizeStep(t *testing.T) {
	r, ep, _ := setup(t)
	ctx := context.Background()
	root := t.TempDir()
	os.MkdirAll(ep.WorkDir, 0o755)
	id, err := r.Store.UpsertCharacter(ctx, db.UpsertCharacterParams{SeriesID: ep.SeriesID, Name: "Bandit", Strategy: "A", RefPath: "refs/Bandit.wav", Speed: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i, txt := range []string{"Ahoj", "Jdeme", ""} {
		r.Store.InsertSegment(ctx, db.InsertSegmentParams{EpisodeID: ep.ID, Idx: int64(i), StartS: float64(i), EndS: float64(i) + 1})
		r.Store.SetSegmentCS(ctx, db.SetSegmentCSParams{TextCs: txt, EpisodeID: ep.ID, Idx: int64(i)})
	}
	r.Store.InsertSegment(ctx, db.InsertSegmentParams{EpisodeID: ep.ID, Idx: 3, StartS: 4, EndS: 5}) // no character
	r.Store.SetSegmentCS(ctx, db.SetSegmentCSParams{TextCs: "Kdo?", EpisodeID: ep.ID, Idx: 3})
	for i := range 3 {
		r.Store.DB.Exec(`UPDATE segments SET character_id = ? WHERE episode_id = ? AND idx = ?`, id, ep.ID, i)
	}

	f := &fakeTTS{dir: t.TempDir()}
	var logs bytes.Buffer
	step := Synthesize{Store: r.Store, Root: root, TTS: f, Parallel: 2, Log: slog.New(slog.NewTextHandler(&logs, nil))}
	if _, err := step.Run(ctx, ep); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 2 {
		t.Fatalf("tts calls %d, want 2 (empty text and missing reference are skipped)", f.calls.Load())
	}
	segs, _ := r.Store.ListSegments(ctx, ep.ID)
	if segs[0].TtsDuration == nil || *segs[0].TtsDuration != 0.4 || segs[1].TtsPath == "" || segs[2].TtsPath != "" {
		t.Fatalf("%+v", segs)
	}
	if !bytes.Contains(logs.Bytes(), []byte("bez reference")) {
		t.Fatal("missing reference not reported")
	}

	// Rerun: nothing is synthesized again; a changed translation is.
	r.Store.SetSegmentCS(ctx, db.SetSegmentCSParams{TextCs: "Jdeme ven", EpisodeID: ep.ID, Idx: 1})
	if _, err := step.Run(ctx, ep); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 3 {
		t.Fatalf("after rerun %d calls, want 3", f.calls.Load())
	}
}
