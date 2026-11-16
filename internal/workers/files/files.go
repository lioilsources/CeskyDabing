// Package files is the file API every worker shares (POST/GET /v1/files) and
// the error type for non-2xx worker responses. It implements storage.Transport
// with plain HTTP, so it streams large WAVs instead of buffering them.
package files

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Client struct {
	Worker  string // name for error messages
	BaseURL string
	HTTP    *http.Client
}

func (c Client) Upload(ctx context.Context, local string) (string, string, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/v1/files"), pr)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", c.Error(resp.StatusCode, body)
	}
	var ref struct{ Path, Dir string }
	if err := json.Unmarshal(body, &ref); err != nil {
		return "", "", fmt.Errorf("worker %s: upload: %w", c.Worker, err)
	}
	return ref.Path, ref.Dir, nil
}

func (c Client) Download(ctx context.Context, remote string, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/v1/files?path="+url.QueryEscape(remote)), nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return c.Error(resp.StatusCode, body)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

// Error turns a worker error response into an error; FastAPI puts the message in "detail".
func (c Client) Error(status int, body []byte) error {
	var e struct {
		Detail json.RawMessage `json:"detail"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &e) == nil && len(e.Detail) > 0 {
		var s string
		if json.Unmarshal(e.Detail, &s) == nil {
			msg = s
		} else {
			msg = string(e.Detail)
		}
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return fmt.Errorf("worker %s: HTTP %d: %s", c.Worker, status, msg)
}

func (c Client) url(path string) string {
	return strings.TrimRight(c.BaseURL, "/") + path
}
