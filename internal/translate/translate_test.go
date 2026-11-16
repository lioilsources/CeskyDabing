package translate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// fakeLLM answers each call with the next scripted reply; "" echoes every id
// from the request as "cs-<id>".
type fakeLLM struct {
	replies []string
	model   string
	calls   []string
}

var reID = regexp.MustCompile(`"id": (\d+)`)

func (f *fakeLLM) Complete(_ context.Context, msgs []Message) (string, string, error) {
	user := msgs[len(msgs)-1].Content
	f.calls = append(f.calls, user)
	reply := ""
	if len(f.replies) > 0 {
		reply, f.replies = f.replies[0], f.replies[1:]
	}
	if reply == "" {
		var lines []string
		for _, m := range reID.FindAllStringSubmatch(user, -1) {
			lines = append(lines, fmt.Sprintf(`{"id":%s,"cs":"cs-%s"}`, m[1], m[1]))
		}
		reply = `{"lines":[` + strings.Join(lines, ",") + `]}`
	}
	return reply, f.model, nil
}

func lines(spans ...[2]float64) []Line {
	var out []Line
	for i, s := range spans {
		out = append(out, Line{ID: i, Start: s[0], End: s[1], Character: "Bandit", EN: fmt.Sprintf("line %d", i), MaxChars: MaxChars(s[0], s[1])})
	}
	return out
}

func TestScenes(t *testing.T) {
	got := Scenes(lines([2]float64{0, 1}, [2]float64{1.5, 2}, [2]float64{10, 11}, [2]float64{11, 12}))
	if len(got) != 2 || len(got[0]) != 2 || got[1][0].ID != 2 {
		t.Fatalf("%+v", got)
	}
	many := make([]Line, MaxBatch+1)
	if n := len(Scenes(many)); n != 2 {
		t.Fatalf("batch limit: %d scenes", n)
	}
}

func TestTranslateContextGlossaryAndRetry(t *testing.T) {
	f := &fakeLLM{replies: []string{"", "not json at all", "<think>hmm</think>\n```json\n{\"lines\":[{\"id\":2,\"cs\":\"Ahoj\"}]}\n```"}}
	tr := &Translator{LLM: f, Glossary: &Glossary{Terms: []Term{{EN: "Bingo", CS: "Bingo", Note: "nesklonňovat"}}}}
	got, err := tr.Translate(context.Background(), lines([2]float64{0, 1}, [2]float64{1, 2}, [2]float64{20, 21}))
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "cs-0" || got[1] != "cs-1" || got[2] != "Ahoj" {
		t.Fatalf("%v", got)
	}
	if len(f.calls) != 3 {
		t.Fatalf("want a retry after the bad answer, calls: %d", len(f.calls))
	}
	if !strings.Contains(f.calls[0], "Bingo → Bingo (nesklonňovat)") {
		t.Fatal("glossary missing in prompt")
	}
	if !strings.Contains(f.calls[1], "Bandit: cs-1") {
		t.Fatal("previous scene missing as context")
	}
}

func TestTranslateMissingLineAndModelCheck(t *testing.T) {
	f := &fakeLLM{replies: []string{`{"lines":[{"id":0,"cs":"a"}]}`, `{"lines":[]}`}}
	tr := &Translator{LLM: f}
	if _, err := tr.Translate(context.Background(), lines([2]float64{0, 1}, [2]float64{1, 2})); err == nil {
		t.Fatal("missing line must fail after the retry")
	}
	f = &fakeLLM{model: "fallback"}
	tr = &Translator{LLM: f, CheckModel: func(m string) error {
		if m == "fallback" {
			return errors.New("fallback")
		}
		return nil
	}}
	if _, err := tr.Translate(context.Background(), lines([2]float64{0, 1})); err == nil {
		t.Fatal("answer from the fallback model must fail")
	}
}

func TestShorten(t *testing.T) {
	f := &fakeLLM{replies: []string{`{"lines":[{"id":7,"cs":"Krátce."}]}`}}
	tr := &Translator{LLM: f}
	got, err := tr.Shorten(context.Background(), Line{ID: 7, EN: "x", MaxChars: 10}, "Velmi dlouhá replika")
	if err != nil || got != "Krátce." || !strings.Contains(f.calls[0], "nejvýš 10 znaků") {
		t.Fatalf("%q %v", got, err)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPLLM(t *testing.T) {
	var sent map[string]any
	l := NewHTTPLLM("http://llm.example/v1/", "openclaw-default", "k", 0)
	l.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://llm.example/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("request %s %v", r.URL, r.Header)
		}
		json.NewDecoder(r.Body).Decode(&sent)
		body := `{"model":"qwen36-agent","choices":[{"message":{"content":"hi"}}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	text, model, err := l.Complete(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil || text != "hi" || model != "qwen36-agent" || sent["model"] != "openclaw-default" {
		t.Fatalf("%q %q %v %v", text, model, err, sent)
	}
}
