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

package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFromEnvNilWithoutKey(t *testing.T) {
	if c := FromEnv(func(string) string { return "" }); c != nil {
		t.Fatalf("want nil client without LLM_API_KEY, got %v", c)
	}
}

func TestFromEnvDefaultsAndOverrides(t *testing.T) {
	env := map[string]string{"LLM_API_KEY": "k"}
	c := FromEnv(func(s string) string { return env[s] }).(*OpenAICompat)
	if c.BaseURL != "https://openrouter.ai/api/v1" || c.Model != "anthropic/claude-sonnet-4" {
		t.Fatalf("defaults wrong: %+v", c)
	}
	env["LLM_BASE_URL"] = "https://api.example/v1/"
	env["LLM_MODEL"] = "deepseek/deepseek-chat"
	c = FromEnv(func(s string) string { return env[s] }).(*OpenAICompat)
	if c.BaseURL != "https://api.example/v1" || c.Model != "deepseek/deepseek-chat" {
		t.Fatalf("overrides wrong: %+v", c)
	}
}

func TestComplete(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		var in struct {
			Model    string                           `json:"model"`
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		_ = json.Unmarshal(b, &in)
		gotModel = in.Model
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "  a review  "}}},
		})
	}))
	defer srv.Close()
	c := &OpenAICompat{BaseURL: srv.URL, APIKey: "sekret", Model: "m", HTTP: srv.Client()}
	out, err := c.Complete(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "a review" {
		t.Fatalf("out = %q, want trimmed content", out)
	}
	if gotAuth != "Bearer sekret" || gotModel != "m" {
		t.Fatalf("auth=%q model=%q", gotAuth, gotModel)
	}
}

func TestCompleteErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "rate limited"}})
	}))
	defer srv.Close()
	c := &OpenAICompat{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client()}
	_, err := c.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("want rate-limit error, got %v", err)
	}
}
