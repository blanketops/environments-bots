/*
Copyright 2026 The BlanketOps Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package llm is a small client for any OpenAI-compatible chat completions
// API (OpenRouter, DeepSeek, Together, a local server, or an Anthropic shim).
// Bots use it for optional, advisory model passes; it is never on a blocking
// path, and when no API key is configured there is no client at all and the
// caller falls back to its deterministic behaviour.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client completes a chat prompt and returns the assistant's text.
type Client interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// OpenAICompat talks to an OpenAI-compatible /chat/completions endpoint.
type OpenAICompat struct {
	BaseURL string // e.g. https://openrouter.ai/api/v1
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// FromEnv builds a client from LLM_API_KEY, LLM_BASE_URL and LLM_MODEL, or
// returns nil when LLM_API_KEY is unset — the caller then stays deterministic.
// Defaults: OpenRouter, Claude Sonnet (strong code review per dollar). The
// return type is the Client interface so a no-key result is a true nil.
func FromEnv(getenv func(string) string) Client {
	key := strings.TrimSpace(getenv("LLM_API_KEY"))
	if key == "" {
		return nil
	}
	base := strings.TrimSpace(getenv("LLM_BASE_URL"))
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	model := strings.TrimSpace(getenv("LLM_MODEL"))
	if model == "" {
		model = "anthropic/claude-sonnet-4"
	}
	return &OpenAICompat{
		BaseURL: strings.TrimRight(base, "/"),
		APIKey:  key,
		Model:   model,
		HTTP:    &http.Client{Timeout: 90 * time.Second},
	}
}

// Complete sends the system and user messages and returns the reply text.
func (c *OpenAICompat) Complete(ctx context.Context, system, user string) (string, error) {
	payload := map[string]any{
		"model":       c.Model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode %s response: %w", c.Model, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := out.Error.Message
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("%s: HTTP %d: %s", c.Model, resp.StatusCode, msg)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%s: no choices in response", c.Model)
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
