package separate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Worker obaluje generovaného klienta: chybové odpovědi převádí na error
// a implementuje storage.Transport pro upload fallback.
type Worker struct {
	c *ClientWithResponses
}

func New(url string, timeout time.Duration) (*Worker, error) {
	c, err := NewClientWithResponses(url, WithHTTPClient(&http.Client{Timeout: timeout}))
	if err != nil {
		return nil, err
	}
	return &Worker{c: c}, nil
}

func (w *Worker) Health(ctx context.Context) (*Health, error) {
	r, err := w.c.HealthWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, apiError(r.HTTPResponse, r.Body, nil)
	}
	return r.JSON200, nil
}

func (w *Worker) Separate(ctx context.Context, req SeparateRequest) (*SeparateResult, error) {
	r, err := w.c.SeparateWithResponse(ctx, req)
	if err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, apiError(r.HTTPResponse, r.Body, r.JSONDefault)
	}
	return r.JSON200, nil
}

func (w *Worker) Upload(ctx context.Context, local string) (string, string, error) {
	f, err := os.Open(local)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", filepath.Base(local))
		if err == nil {
			_, err = io.Copy(part, f)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	r, err := w.c.UploadFileWithBodyWithResponse(ctx, mw.FormDataContentType(), pr)
	if err != nil {
		return "", "", err
	}
	if r.JSON200 == nil {
		return "", "", apiError(r.HTTPResponse, r.Body, r.JSONDefault)
	}
	return r.JSON200.Path, r.JSON200.Dir, nil
}

func (w *Worker) Download(ctx context.Context, remote string, dst io.Writer) error {
	// Bez *WithResponse, ať se velký WAV nenačítá celý do paměti.
	resp, err := w.c.ClientInterface.DownloadFile(ctx, &DownloadFileParams{Path: remote})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return apiError(resp, body, nil)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

func apiError(resp *http.Response, body []byte, e *Error) error {
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	if e != nil && e.Detail != nil {
		if b, err := json.Marshal(e.Detail); err == nil {
			return fmt.Errorf("worker separate: HTTP %d: %s", status, b)
		}
	}
	if len(body) > 500 {
		body = body[:500]
	}
	return fmt.Errorf("worker separate: HTTP %d: %s", status, body)
}
