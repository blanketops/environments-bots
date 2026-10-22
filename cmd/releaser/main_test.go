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
		{"prepare -h", []string{"prepare", "-h"}, 0, "", "-ref"},
		{"complete has no -ref", []string{"complete", "-ref", "x"}, 2, "", "-ref"},
		{"bad flag", []string{"verify", "-nope"}, 2, "", "-nope"},
		{"extra argument", []string{"verify", "-release", "v0.0.1", "x"}, 2, "", `unexpected argument "x"`},
		{"no release", []string{"verify"}, 2, "", "-release is required"},
		{"bad project", []string{"verify", "-release", "v0.0.1", "-project", "blanketops"}, 2, "", "not org/number"},
		{"no token", []string{"verify", "-release", "v0.0.1", "-repo", "o/r"}, 1, "", "no token"},
		{"bad repo", []string{"verify", "-release", "v0.0.1", "-repo", "bad", "-dry-run"}, 1, "", "not owner/name"},
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

func TestCommands(t *testing.T) {
	srv := githubtest.New("blanketops", "environments-bots", "tok")
	t.Cleanup(srv.Close)
	srv.StatusOptions["Release"], srv.StatusOptions["Done"] = "opt-release", "opt-done"
	srv.RefCommits["develop"] = []githubtest.Commit{{SHA: "2222222", Message: "feat(releaser): add"}, {SHA: "1111111", Message: "Initial commit"}}
	summary := filepath.Join(t.TempDir(), "summary.md")
	e := env{"GH_TOKEN": "tok", "GITHUB_REPOSITORY": "blanketops/environments-bots", "GITHUB_API_URL": srv.URL,
		"GITHUB_STEP_SUMMARY": summary, "GITHUB_SERVER_URL": "https://github.com", "GITHUB_RUN_ID": "7"}

	code, stdout, stderr := runCmd(t, e, "prepare", "-release", "v0.0.1", "-project", "blanketops/1")
	if code != 0 || !strings.Contains(stdout, "v0.0.1 prepare: passed") || !strings.Contains(stderr, "warning: 1111111 does not follow") {
		t.Fatalf("prepare: %d\n%s\n%s", code, stdout, stderr)
	}
	is := srv.Issue("Release: v0.0.1")
	if is == nil || !strings.Contains(is.Comments[0], "https://github.com/blanketops/environments-bots/actions/runs/7") {
		t.Fatalf("issue = %+v", is)
	}
	if data, err := os.ReadFile(summary); err != nil || !strings.Contains(string(data), "prepare: passed") {
		t.Errorf("summary file: %v %q", err, data)
	}

	if code, _, stderr = runCmd(t, e, "verify", "-release", "v0.0.1"); code != 0 {
		t.Errorf("verify: %d %s", code, stderr)
	}

	code, _, stderr = runCmd(t, e, "complete", "-release", "v0.0.1", "-project", "blanketops/1")
	if code != 1 || !strings.Contains(stderr, "Tag: v0.0.1 is not tagged") || is.State != "open" {
		t.Errorf("complete before the release: %d %q, issue %s", code, stderr, is.State)
	}
	srv.Tags = []githubtest.Tag{{Name: "v0.0.1", SHA: "abc"}}
	srv.Releases["v0.0.1"] = githubtest.Release{}
	if code, _, stderr = runCmd(t, e, "complete", "-release", "v0.0.1", "-project", "blanketops/1"); code != 0 || is.State != "closed" {
		t.Errorf("complete: %d %q, issue %s", code, stderr, is.State)
	}

	// The command fails when it cannot run; a bad summary path too.
	if code, _, stderr = runCmd(t, e, "verify", "-release", "x"); code != 1 || !strings.Contains(stderr, "not a release version") {
		t.Errorf("bad release: %d %q", code, stderr)
	}
	e["GITHUB_STEP_SUMMARY"] = t.TempDir()
	if code, _, stderr = runCmd(t, e, "verify", "-release", "v0.0.2"); code != 1 || !strings.Contains(stderr, "is a directory") {
		t.Errorf("bad summary: %d %q", code, stderr)
	}
}

func TestEmitAndRunURL(t *testing.T) {
	var out bytes.Buffer
	if err := emit(&out, "", "x"); err != nil || out.String() != "x" {
		t.Errorf("emit without file: %v %q", err, out.String())
	}
	if runURL(env{}.get) != "" {
		t.Error("runURL without env")
	}
}
