package asr

import (
	"context"
	"net/http"
	"time"

	"github.com/lioilsources/CeskyDabing/internal/workers/files"
)

// Worker wraps the generated client; see separate.Worker.
type Worker struct {
	files.Client
	c *ClientWithResponses
}

func New(url string, timeout time.Duration) (*Worker, error) {
	hc := &http.Client{Timeout: timeout}
	c, err := NewClientWithResponses(url, WithHTTPClient(hc))
	if err != nil {
		return nil, err
	}
	return &Worker{Client: files.Client{Worker: "asr", BaseURL: url, HTTP: hc}, c: c}, nil
}

func (w *Worker) Health(ctx context.Context) (*Health, error) {
	r, err := w.c.HealthWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, w.Error(r.StatusCode(), r.Body)
	}
	return r.JSON200, nil
}

func (w *Worker) Transcribe(ctx context.Context, req TranscribeRequest) (*TranscribeResult, error) {
	r, err := w.c.TranscribeWithResponse(ctx, req)
	if err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, w.Error(r.StatusCode(), r.Body)
	}
	return r.JSON200, nil
}
