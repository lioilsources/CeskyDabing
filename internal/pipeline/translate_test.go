package pipeline

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/lioilsources/CeskyDabing/internal/store/db"
	"github.com/lioilsources/CeskyDabing/internal/translate"
)

type echoLLM struct{ model string }

var reLineID = regexp.MustCompile(`"id": (\d+)`)

func (e echoLLM) Complete(_ context.Context, msgs []translate.Message) (string, string, error) {
	var lines []string
	for _, m := range reLineID.FindAllStringSubmatch(msgs[len(msgs)-1].Content, -1) {
		lines = append(lines, fmt.Sprintf(`{"id":%s,"cs":"Řádek %s"}`, m[1], m[1]))
	}
	return `{"lines":[` + strings.Join(lines, ",") + `]}`, e.model, nil
}

func TestTranslateStep(t *testing.T) {
	r, ep, _ := setup(t)
	ctx := context.Background()
	if err := os.MkdirAll(ep.WorkDir, 0o755); err != nil { // the runner creates it normally
		t.Fatal(err)
	}
	for i, txt := range []string{"Hello!", "Let's play."} {
		if err := r.Store.InsertSegment(ctx, db.InsertSegmentParams{EpisodeID: ep.ID, Idx: int64(i), StartS: float64(i), EndS: float64(i) + 1.5, TextEn: txt}); err != nil {
			t.Fatal(err)
		}
	}
	step := Translate{Store: r.Store, Root: t.TempDir(), LLM: echoLLM{model: "qwen36-agent"}}
	if _, err := step.Run(ctx, ep); err != nil {
		t.Fatal(err)
	}
	segs, _ := r.Store.ListSegments(ctx, ep.ID)
	if segs[0].TextCs != "Řádek 0" || segs[1].TextCs != "Řádek 1" {
		t.Fatalf("%+v", segs)
	}
	var jobs int
	r.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE step = 'translate' AND worker = 'llm' AND status = 'ok'`).Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("llm calls logged: %d", jobs)
	}

	step.LLM = echoLLM{model: "fallback"}
	if _, err := step.Run(ctx, ep); err == nil || !strings.Contains(err.Error(), "záložním modelem") {
		t.Fatalf("fallback answer must fail: %v", err)
	}
}
