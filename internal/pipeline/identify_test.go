package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/workers/asr"
)

func writeTranscript(t *testing.T, dir string, tr asr.Transcript) {
	t.Helper()
	b, _ := json.Marshal(tr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileSegments), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIdentifyStep(t *testing.T) {
	r, ep, _ := setup(t)
	ctx := context.Background()
	root := t.TempDir()
	step := Identify{Store: r.Store, Root: root, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	f0 := 120.0
	writeTranscript(t, ep.WorkDir, asr.Transcript{Speakers: []asr.Speaker{
		{Id: "SPK_00", Embedding: []float32{1, 0}, F0Mean: &f0},
		{Id: "SPK_01", Embedding: []float32{0, 1}},
	}})
	for i, spk := range []string{"SPK_00", "SPK_01", "SPK_00"} {
		if err := r.Store.InsertSegment(ctx, db.InsertSegmentParams{EpisodeID: ep.ID, Idx: int64(i), Speaker: spk}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := step.Run(ctx, ep); err != nil {
		t.Fatal(err)
	}
	castPath := filepath.Join(SeriesDir(root, "s"), FileCast)
	chars, _ := r.Store.ListCharacters(ctx, ep.SeriesID)
	if len(chars) != 2 || chars[0].Name != "UNKNOWN_1" || chars[0].F0Mean == nil || *chars[0].F0Mean != 120 {
		t.Fatalf("characters: %+v", chars)
	}
	segs, _ := r.Store.ListSegments(ctx, ep.ID)
	if segs[0].CharacterID == nil || *segs[0].CharacterID != chars[0].ID || *segs[1].CharacterID != chars[1].ID {
		t.Fatalf("segments not linked: %+v", segs)
	}

	// The user renames UNKNOWN_1 in cast.yaml; a rerun follows the file.
	b, _ := os.ReadFile(castPath)
	if err := os.WriteFile(castPath, []byte(strings.Replace(string(b), "UNKNOWN_1", "Bandit", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := step.Run(ctx, ep); err != nil {
		t.Fatal(err)
	}
	chars, _ = r.Store.ListCharacters(ctx, ep.SeriesID)
	if len(chars) != 2 || chars[0].Name != "Bandit" || chars[0].CentroidCount != 1 {
		t.Fatalf("after rename: %+v", chars)
	}
	segs, _ = r.Store.ListSegments(ctx, ep.ID)
	if *segs[0].CharacterID != chars[0].ID {
		t.Fatalf("segment not relinked to Bandit: %+v", segs[0])
	}
}
