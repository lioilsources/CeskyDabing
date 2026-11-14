package tts

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func newClient(t *testing.T, rights string, fn roundTrip) *AiStack {
	a := NewAiStack("http://audio.example", "", rights, "test", time.Second)
	a.HTTP.Transport = fn
	a.sleep = func(context.Context, time.Duration) error { return nil }
	return a
}

func TestVoiceID(t *testing.T) {
	id := VoiceID("Prasátko Pepa a přátelé", "UNKNOWN_12 (táta)", []byte("x"))
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,47}$`).MatchString(id) {
		t.Fatalf("invalid voice id %q", id)
	}
	if VoiceID("s", "c", []byte("a")) == VoiceID("s", "c", []byte("b")) {
		t.Fatal("a new reference must give a new id")
	}
}

func TestEnsureVoice(t *testing.T) {
	ref := filepath.Join(t.TempDir(), "ref.wav")
	os.WriteFile(ref, []byte("RIFF"), 0o644)
	v := Voice{Series: "bluey", Character: "Bandit", RefPath: ref, Language: "cs"}

	var uploaded bool
	a := newClient(t, "", func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			uploaded = true
		}
		return reply(404, `{"detail":"neexistuje"}`), nil
	})
	if _, err := a.EnsureVoice(context.Background(), v); err == nil || !strings.Contains(err.Error(), "DUB_TTS_VOICE_RIGHTS") || uploaded {
		t.Fatalf("without rights nothing may be uploaded: %v", err)
	}

	var form string
	a = newClient(t, "own", func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return reply(404, ""), nil
		}
		r.ParseMultipartForm(1 << 20)
		form = r.FormValue("rights") + "|" + r.FormValue("voice_id")
		return reply(201, "{}"), nil
	})
	id, err := a.EnsureVoice(context.Background(), v)
	if err != nil || form != "own|"+id {
		t.Fatalf("%q %q %v", form, id, err)
	}

	a = newClient(t, "", func(r *http.Request) (*http.Response, error) { return reply(200, "{}"), nil })
	if _, err := a.EnsureVoice(context.Background(), v); err != nil {
		t.Fatalf("an existing voice needs no rights: %v", err)
	}
}

func TestSynthesizePolls(t *testing.T) {
	polls := 0
	a := newClient(t, "own", func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/tts":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"commercial_only":false`) || !strings.Contains(string(b), `"voice":"v1"`) {
				t.Errorf("request %s", b)
			}
			return reply(202, `{"job_id":"j1"}`), nil
		case r.URL.Path == "/v1/audio/jobs/j1":
			polls++
			if polls < 3 {
				return reply(200, `{"status":"running"}`), nil
			}
			return reply(200, `{"status":"done","outputs":[{"url":"/v1/audio/jobs/j1/outputs/a.wav"}]}`), nil
		case r.URL.Path == "/v1/audio/jobs/j1/outputs/a.wav":
			return reply(200, "WAVDATA"), nil
		}
		return reply(500, "unexpected "+r.URL.Path), nil
	})
	wav, err := a.Synthesize(context.Background(), Request{Text: "Ahoj", Language: "cs", VoiceID: "v1"})
	if err != nil || string(wav) != "WAVDATA" || polls != 3 {
		t.Fatalf("%q %v polls=%d", wav, err, polls)
	}

	a = newClient(t, "own", func(r *http.Request) (*http.Response, error) { return reply(503, `{"detail":"xtts neběží"}`), nil })
	if _, err := a.Synthesize(context.Background(), Request{Text: "x"}); err == nil || !strings.Contains(err.Error(), "službu brokeru tts") {
		t.Fatalf("503 must explain the missing service: %v", err)
	}
}
