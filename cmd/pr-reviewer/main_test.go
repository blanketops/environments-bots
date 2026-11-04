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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
)

type env map[string]string

func (e env) get(k string) string { return e[k] }

func runCmd(t *testing.T, e env, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut, e.get)
	return code, out.String(), errOut.String()
}

func TestUsage(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		code               int
		stdout, stderrPart string
	}{
		{"no command", nil, 2, "", "Usage:"},
		{"version", []string{"version"}, 0, "dev\n", ""},
		{"--version", []string{"--version"}, 0, "dev\n", ""},
		{"help", []string{"help"}, 0, "Usage:", ""},
		{"-h", []string{"-h"}, 0, "Usage:", ""},
		{"unknown", []string{"nope"}, 2, "", `unknown command "nope"`},
		{"review -h", []string{"review", "-h"}, 0, "", "-pr"},
		{"bad flag", []string{"review", "-nope"}, 2, "", "-nope"},
		{"no pr", []string{"review", "-repo", "o/r", "-dry-run"}, 2, "", "no pull request"},
		{"bad repo", []string{"review", "-pr", "1", "-repo", "bad", "-dry-run"}, 1, "", "not owner/name"},
		{"no token", []string{"review", "-pr", "1", "-repo", "o/r"}, 1, "", "no token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(t, env{}, tt.args...)
			if code != tt.code {
				t.Errorf("exit = %d, want %d (stderr %q)", code, tt.code, stderr)
			}
			if tt.stdout != "" && !strings.Contains(stdout, tt.stdout) {
				t.Errorf("stdout = %q, want %q", stdout, tt.stdout)
			}
			if tt.stderrPart != "" && !strings.Contains(stderr, tt.stderrPart) {
				t.Errorf("stderr = %q, want %q", stderr, tt.stderrPart)
			}
		})
	}
}

func TestReviewDryRun(t *testing.T) {
	srv := githubtest.New("blanketops", "environments", "tok")
	srv.Login = "blanketops-environments-pr-reviewer[bot]"
	srv.Pulls = append(srv.Pulls, githubtest.Pull{
		Number: 4,
		Title:  "fix: resolve issue",
		State:  "open",
		Base:   "develop",
		Head:   "fix/resolve",
	})
	srv.PullFiles[4] = []github.PullFile{
		{Filename: "README.md", Status: "modified", Additions: 3, Deletions: 1},
	}
	t.Cleanup(srv.Close)

	summary := filepath.Join(t.TempDir(), "summary.md")
	e := env{
		"GH_TOKEN":            "tok",
		"GITHUB_REPOSITORY":   "blanketops/environments",
		"GITHUB_API_URL":      srv.URL,
		"GITHUB_STEP_SUMMARY": summary,
		"GITHUB_SERVER_URL":   "https://github.com",
		"GITHUB_RUN_ID":       "7",
	}

	code, stdout, stderr := runCmd(t, e, "review", "-pr", "4", "-repo", "blanketops/environments", "-api-url", srv.URL, "-summary", summary)
	if code != 0 {
		t.Fatalf("review: %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "PR #4 review") {
		t.Errorf("stdout = %q, want PR review summary", stdout)
	}
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(data), "PR #4 review") {
		t.Errorf("summary file = %q", data)
	}
}
