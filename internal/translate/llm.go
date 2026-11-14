// Package translate turns English segments into spoken Czech that fits the
// time slot, through an OpenAI-compatible LLM endpoint (the AiStack gateway).
package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LLM completes a chat. It returns the text and the model that answered.
type LLM interface {
	Complete(ctx context.Context, msgs []Message) (text, model string, err error)
}

// HTTPLLM talks to /chat/completions of an OpenAI-compatible server.
type HTTPLLM struct {
	BaseURL string // e.g. http://host:8080/v1
	Model   string
	APIKey  string
	HTTP    *http.Client
}

func NewHTTPLLM(baseURL, model, apiKey string, timeout time.Duration) *HTTPLLM {
	return &HTTPLLM{BaseURL: baseURL, Model: model, APIKey: apiKey, HTTP: &http.Client{Timeout: timeout}}
}

func (l *HTTPLLM) Complete(ctx context.Context, msgs []Message) (string, string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":       l.Model,
		"messages":    msgs,
		"temperature": 0.3,
		// Qwen3 style models: no reasoning trace for a translation.
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(l.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if l.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.APIKey)
	}
	resp, err := l.HTTP.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		if len(b) > 500 {
			b = b[:500]
		}
		return "", "", fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", "", fmt.Errorf("llm: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", out.Model, fmt.Errorf("llm: prázdná odpověď")
	}
	return out.Choices[0].Message.Content, out.Model, nil
}

var (
	reThink = regexp.MustCompile(`(?s)<think>.*?</think>`)
	reFence = regexp.MustCompile("(?s)^```(?:json)?\\s*(.*?)\\s*```$")
)

// extractJSON strips a reasoning trace and a code fence around the JSON answer.
func extractJSON(s string) string {
	s = strings.TrimSpace(reThink.ReplaceAllString(s, ""))
	if m := reFence.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	if i, j := strings.IndexAny(s, "{["), strings.LastIndexAny(s, "}]"); i >= 0 && j > i {
		s = s[i : j+1]
	}
	return s
}
