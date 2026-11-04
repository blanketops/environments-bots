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

	"github.com/blanketops/environments-bots/internal/develop"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestRunUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		out  string // substring expected on stdout
		err  string // substring expected on stderr
	}{
		{"no args", nil, 2, "", "Usage:"},
		{"help", []string{"help"}, 0, "Usage:", ""},
		{"version flag", []string{"-v"}, 0, "dev", ""},
		{"version command", []string{"version"}, 0, "dev", ""},
		{"unknown", []string{"bogus"}, 2, "", "unknown command"},
		{"sync bad flag", []string{"sync", "-nope"}, 2, "", ""},
		{"sync extra arg", []string{"sync", "x"}, 2, "", "unexpected argument"},
		{"open in usage", []string{"help"}, 0, "open", ""},
		{"open no branch", []string{"open", "-repo", "o/r", "-dry-run"}, 2, "", "no branch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(tc.args, &out, &errb, env(nil))
			if code != tc.code {
				t.Fatalf("code = %d, want %d (stderr %q)", code, tc.code, errb.String())
			}
			if tc.out != "" && !strings.Contains(out.String(), tc.out) {
				t.Fatalf("stdout = %q, want %q", out.String(), tc.out)
			}
			if tc.err != "" && !strings.Contains(errb.String(), tc.err) {
				t.Fatalf("stderr = %q, want %q", errb.String(), tc.err)
			}
		})
	}
}

func TestRunSyncNoToken(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"sync", "-repo", "o/r"}, &out, &errb, env(nil))
	if code != 1 || !strings.Contains(errb.String(), "no token") {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
}

func TestRunSyncOpensPullAndWritesSummary(t *testing.T) {
	s := githubtest.New("blanketops", "environments-bots", "t0ken")
	s.Branches["main"] = "mainhead0"
	s.Branches["develop"] = "devhead00"
	s.Compares["develop...main"] = []githubtest.Commit{{SHA: "aaaaaa1", Message: "feat: thing"}}
	defer s.Close()

	summaryFile := filepath.Join(t.TempDir(), "summary.md")
	var out, errb bytes.Buffer
	code := run(
		[]string{"sync", "-repo", "blanketops/environments-bots", "-api-url", s.URL, "-summary", summaryFile},
		&out, &errb,
		env(map[string]string{"GH_TOKEN": "t0ken"}),
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if len(s.Pulls) != 1 {
		t.Fatalf("want one pull request, got %d", len(s.Pulls))
	}
	if !strings.Contains(out.String(), "Opened") {
		t.Fatalf("stdout = %q", out.String())
	}
	data, err := os.ReadFile(summaryFile)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(data), "Opened") {
		t.Fatalf("summary file = %q", data)
	}
}

func TestRunSyncDryRunNeedsNoToken(t *testing.T) {
	s := githubtest.New("blanketops", "environments-bots", "")
	s.Branches["main"] = "mainhead0"
	s.Branches["develop"] = "devhead00"
	s.Compares["develop...main"] = []githubtest.Commit{{SHA: "aaaaaa1", Message: "feat: thing"}}
	defer s.Close()

	var out, errb bytes.Buffer
	code := run(
		[]string{"sync", "-repo", "blanketops/environments-bots", "-api-url", s.URL, "-dry-run"},
		&out, &errb, env(nil),
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if len(s.Pulls) != 0 {
		t.Fatalf("dry run opened %d pull request(s)", len(s.Pulls))
	}
	if !strings.Contains(out.String(), "Would open") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunOpen(t *testing.T) {
	s := githubtest.New("blanketops", "environments-bots", "t0ken")
	s.Branches["develop"] = "devhead00"
	s.Branches["feat/x"] = "branchhd0"
	s.Compares["develop...feat/x"] = []githubtest.Commit{{SHA: "c0ffee1", Message: "feat: add x"}}
	defer s.Close()

	var out, errb bytes.Buffer
	code := run(
		[]string{"open", "-repo", "blanketops/environments-bots", "-api-url", s.URL, "-branch", "feat/x"},
		&out, &errb, env(map[string]string{"GH_TOKEN": "t0ken"}),
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if len(s.Pulls) != 1 || len(s.Issues) != 1 {
		t.Fatalf("want one pull request and one issue, got pulls=%d issues=%d", len(s.Pulls), len(s.Issues))
	}
	if !strings.Contains(out.String(), "Opened") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunOpenBranchFromEnv(t *testing.T) {
	s := githubtest.New("blanketops", "environments-bots", "")
	s.Branches["develop"] = "devhead00"
	s.Branches["fix/y"] = "branchhd1"
	s.Compares["develop...fix/y"] = []githubtest.Commit{{SHA: "c0ffee2", Message: "fix: y"}}
	defer s.Close()

	var out, errb bytes.Buffer
	code := run(
		[]string{"open", "-repo", "blanketops/environments-bots", "-api-url", s.URL, "-dry-run"},
		&out, &errb, env(map[string]string{"GITHUB_REF_NAME": "fix/y"}),
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "fix/y") {
		t.Fatalf("stdout = %q, want the branch from $GITHUB_REF_NAME", out.String())
	}
}

func TestRunResolve(t *testing.T) {
	s := githubtest.New("blanketops", "environments-bots", "t0ken")
	s.Pulls = append(s.Pulls, githubtest.Pull{Number: 9, Title: "feat: z", State: "closed", Base: "develop", Head: "feat/z", Merged: true})
	s.AddIssue(githubtest.Issue{Title: "[pr] feat/z", State: "open", Labels: []string{develop.TrackLabel}})
	defer s.Close()

	var out, errb bytes.Buffer
	code := run(
		[]string{"resolve", "-repo", "blanketops/environments-bots", "-api-url", s.URL, "-pull", "9"},
		&out, &errb, env(map[string]string{"GH_TOKEN": "t0ken"}),
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if is := s.Issue("[pr] feat/z"); is == nil || is.State != "closed" {
		t.Fatalf("issue not closed: %+v", is)
	}
	if !strings.Contains(out.String(), "Closed") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunResolvePullFromRef(t *testing.T) {
	s := githubtest.New("blanketops", "environments-bots", "")
	s.Pulls = append(s.Pulls, githubtest.Pull{Number: 12, Title: "feat: q", State: "closed", Base: "develop", Head: "feat/q", Merged: true})
	s.AddIssue(githubtest.Issue{Title: "[pr] feat/q", State: "open", Labels: []string{develop.TrackLabel}})
	defer s.Close()

	var out, errb bytes.Buffer
	code := run(
		[]string{"resolve", "-repo", "blanketops/environments-bots", "-api-url", s.URL, "-dry-run"},
		&out, &errb, env(map[string]string{"GITHUB_REF": "refs/pull/12/merge"}),
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "#12") {
		t.Fatalf("stdout = %q, want pull 12 from $GITHUB_REF", out.String())
	}
}
