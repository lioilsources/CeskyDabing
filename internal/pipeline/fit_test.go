package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/translate"
)

func TestFitSlot(t *testing.T) {
	for _, c := range []struct{ start, end, next, want float64 }{
		{0, 2, 0, 3},      // last line: one second of overflow
		{0, 2, 2.5, 2.45}, // next line close: stop before it
		{0, 2, 2.01, 2},   // next line right after: own span only
		{10, 12, 20, 3},   // far next line: capped overflow
	} {
		if got := FitSlot(c.start, c.end, c.next); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("FitSlot(%v,%v,%v) = %v, want %v", c.start, c.end, c.next, got, c.want)
		}
	}
}

func TestDecideFit(t *testing.T) {
	for _, c := range []struct {
		d, slot float64
		iter    int
		action  FitAction
		factor  float64
	}{
		{1.0, 2, 0, FitKeep, 1},
		{2.2, 2, 0, FitStretch, 2 / 2.2}, // within 20 %: stretch exactly into the slot
		{3.0, 2, 0, FitShorten, 1},       // too long: shorten first
		{3.0, 2, 2, FitStretch, 1 / 1.2}, // no shortenings left: stretch the maximum
	} {
		a, f := DecideFit(c.d, c.slot, c.iter)
		if a != c.action || math.Abs(f-c.factor) > 1e-9 {
			t.Errorf("DecideFit(%v,%v,%d) = %v,%v want %v,%v", c.d, c.slot, c.iter, a, f, c.action, c.factor)
		}
	}
}

type copyStretcher struct{ factors map[string]float64 }

func (c copyStretcher) Stretch(_ context.Context, in, out string, factor float64) error {
	c.factors[out] = factor
	b, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	return os.WriteFile(out, b, 0o644)
}

// shortLLM halves the line it is asked to shorten.
type shortLLM struct{ calls int }

func (s *shortLLM) Complete(_ context.Context, msgs []translate.Message) (string, string, error) {
	s.calls++
	m := reLineID.FindStringSubmatch(strings.ReplaceAll(msgs[1].Content, `"id":`, `"id": `))
	return `{"lines":[{"id":` + m[1] + `,"cs":"Krátká"}]}`, "qwen36-agent", nil
}

func TestFitStep(t *testing.T) {
	r, ep, _ := setup(t)
	ctx := context.Background()
	os.MkdirAll(ep.WorkDir, 0o755)
	id, _ := r.Store.UpsertCharacter(ctx, db.UpsertCharacterParams{SeriesID: ep.SeriesID, Name: "Bandit", Strategy: "A", RefPath: "refs/Bandit.wav", Speed: 1})
	// fakeTTS speaks 0.1 s per rune: "Ahoj" (0.4 s) fits; the long line (3.8 s) is over 1.2× its 2 s slot.
	for i, txt := range []string{"Ahoj", "Tohle je opravdu moc dlouhá věta navíc"} {
		r.Store.InsertSegment(ctx, db.InsertSegmentParams{EpisodeID: ep.ID, Idx: int64(i), StartS: float64(10 * i), EndS: float64(10*i) + 1, TextEn: "x"})
		r.Store.SetSegmentCS(ctx, db.SetSegmentCSParams{TextCs: txt, EpisodeID: ep.ID, Idx: int64(i)})
		r.Store.DB.Exec(`UPDATE segments SET character_id = ? WHERE idx = ?`, id, i)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	synth := Synthesize{Store: r.Store, Root: t.TempDir(), TTS: &fakeTTS{dir: t.TempDir()}, Parallel: 1, Log: log}
	if _, err := synth.Run(ctx, ep); err != nil {
		t.Fatal(err)
	}
	llm := &shortLLM{}
	st := copyStretcher{factors: map[string]float64{}}
	if _, err := (Fit{Synth: synth, LLM: llm, Stretch: st, Log: log}).Run(ctx, ep); err != nil {
		t.Fatal(err)
	}
	segs, _ := r.Store.ListSegments(ctx, ep.ID)
	if llm.calls != 1 || segs[1].TextCs != "Krátká" || segs[1].FitIter != 1 || segs[0].FitIter != 0 {
		t.Fatalf("calls=%d %+v", llm.calls, segs)
	}
	var sum FitSummary
	b, _ := os.ReadFile(ep.WorkDir + "/" + FileFit)
	json.Unmarshal(b, &sum)
	if sum.Lines != 2 || sum.Shortened != 1 || sum.Overflowing != 0 {
		t.Fatalf("%+v", sum)
	}
	for _, it := range sum.Items {
		if _, err := os.Stat(ep.WorkDir + "/" + it.Path); err != nil {
			t.Fatalf("missing %s", it.Path)
		}
	}
}
