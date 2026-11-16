package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AiStack is the client of the AiStack audio service.
type AiStack struct {
	BaseURL string // e.g. http://host:8093
	APIKey  string
	Engine  string // xtts
	Model   string // xtts-v2
	// Rights and Source are required by the service to store a cloned voice:
	// own | consented | licensed | synthetic. They come from config only.
	Rights, Source string
	HTTP           *http.Client
	Poll           time.Duration
	// Wait between polls; replaced in tests.
	sleep func(context.Context, time.Duration) error
}

func NewAiStack(baseURL, apiKey, rights, source string, timeout time.Duration) *AiStack {
	return &AiStack{
		BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Engine: "xtts", Model: "xtts-v2",
		Rights: rights, Source: source, HTTP: &http.Client{Timeout: timeout}, Poll: time.Second,
	}
}

func (a *AiStack) EnsureVoice(ctx context.Context, v Voice) (string, error) {
	ref, err := os.ReadFile(v.RefPath)
	if err != nil {
		return "", err
	}
	id := VoiceID(v.Series, v.Character, ref)
	status, _, err := a.do(ctx, http.MethodGet, "/v1/audio/voices/"+id, "", nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusOK {
		return id, nil
	}
	if a.Rights == "" || a.Source == "" {
		return "", fmt.Errorf("tts: chybí prohlášení o právech k hlasu (DUB_TTS_VOICE_RIGHTS a DUB_TTS_VOICE_SOURCE); služba bez něj hlas neklonuje")
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, val := range map[string]string{
		"voice_id": id, "rights": a.Rights, "source": a.Source,
		"name": v.Series + " / " + v.Character, "language": v.Language,
	} {
		mw.WriteField(k, val)
	}
	part, _ := mw.CreateFormFile("sample", filepath.Base(v.RefPath))
	part.Write(ref)
	mw.Close()
	status, b, err := a.do(ctx, http.MethodPost, "/v1/audio/voices", mw.FormDataContentType(), &body)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated && status != http.StatusConflict { // 409 = stored meanwhile
		return "", apiErr("voices", status, b)
	}
	return id, nil
}

func (a *AiStack) Synthesize(ctx context.Context, r Request) ([]byte, error) {
	req := map[string]any{
		"text": r.Text, "language": r.Language, "voice": r.VoiceID, "engine": a.Engine, "model": a.Model,
		"commercial_only": false, "format": "wav", "speed": max(r.Speed, 0.5),
	}
	if r.Speed == 0 {
		req["speed"] = 1.0
	}
	if r.Seed != nil {
		req["seed"] = *r.Seed
	}
	b, _ := json.Marshal(req)
	status, body, err := a.do(ctx, http.MethodPost, "/v1/audio/tts", "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if status != http.StatusAccepted {
		return nil, apiErr("tts", status, body)
	}
	var acc struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &acc); err != nil || acc.JobID == "" {
		return nil, fmt.Errorf("tts: odpověď bez job_id: %s", body)
	}
	for {
		status, body, err := a.do(ctx, http.MethodGet, "/v1/audio/jobs/"+acc.JobID, "", nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, apiErr("jobs", status, body)
		}
		var job struct {
			Status  string `json:"status"`
			Error   string `json:"error"`
			Outputs []struct {
				URL string `json:"url"`
			} `json:"outputs"`
		}
		if err := json.Unmarshal(body, &job); err != nil {
			return nil, fmt.Errorf("tts job: %w", err)
		}
		switch job.Status {
		case "done":
			if len(job.Outputs) == 0 {
				return nil, fmt.Errorf("tts job %s: hotovo bez výstupu", acc.JobID)
			}
			status, wav, err := a.do(ctx, http.MethodGet, job.Outputs[0].URL, "", nil)
			if err != nil {
				return nil, err
			}
			if status != http.StatusOK {
				return nil, apiErr("output", status, wav)
			}
			return wav, nil
		case "error":
			return nil, fmt.Errorf("tts job %s: %s", acc.JobID, job.Error)
		}
		if err := a.wait(ctx); err != nil {
			return nil, err
		}
	}
}

func (a *AiStack) wait(ctx context.Context) error {
	if a.sleep != nil {
		return a.sleep(ctx, a.Poll)
	}
	t := time.NewTimer(a.Poll)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// do sends a request to a path (or an absolute URL) and returns status and body.
func (a *AiStack) do(ctx context.Context, method, path, contentType string, body io.Reader) (int, []byte, error) {
	url := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		url = a.BaseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if a.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.APIKey)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, b, err
}

func apiErr(what string, status int, body []byte) error {
	var e struct {
		Detail any `json:"detail"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &e) == nil && e.Detail != nil {
		msg = fmt.Sprint(e.Detail)
	}
	if len(msg) > 400 {
		msg = msg[:400]
	}
	if status == http.StatusServiceUnavailable {
		msg += " (TTS engine neběží — úloha potřebuje službu brokeru tts)"
	}
	return fmt.Errorf("tts %s: HTTP %d: %s", what, status, msg)
}
