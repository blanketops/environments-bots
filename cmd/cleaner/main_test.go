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
	"fmt"
	"strings"
	"testing"

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
		out  string
		err  string
	}{
		{"no args", nil, 2, "", "Usage:"},
		{"help", []string{"help"}, 0, "runs", ""},
		{"version", []string{"version"}, 0, "dev", ""},
		{"unknown", []string{"bogus"}, 2, "", "unknown command"},
		{"runs extra arg", []string{"runs", "x"}, 2, "", "unexpected argument"},
		{"runs no token", []string{"runs", "-repo", "o/r"}, 1, "", "no token"},
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

func TestRunRunsPrunes(t *testing.T) {
	s := githubtest.New("blanketops", "environments-bots", "t0ken")
	for i := 0; i < 4; i++ {
		s.WorkflowRuns = append(s.WorkflowRuns, githubtest.WorkflowRun{
			ID: int64(i), WorkflowID: 1, Status: "completed",
			CreatedAt: fmt.Sprintf("2026-11-%02dT00:00:00Z", 28-i),
		})
	}
	defer s.Close()

	var out, errb bytes.Buffer
	code := run(
		[]string{"runs", "-repo", "blanketops/environments-bots", "-api-url", s.URL, "-keep", "1"},
		&out, &errb, env(map[string]string{"GH_TOKEN": "t0ken"}),
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if len(s.WorkflowRuns) != 1 {
		t.Fatalf("want 1 run left, got %d", len(s.WorkflowRuns))
	}
	if !strings.Contains(out.String(), "Deleted 3") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunRunsDryRunNeedsNoToken(t *testing.T) {
	s := githubtest.New("blanketops", "environments-bots", "")
	s.WorkflowRuns = append(s.WorkflowRuns,
		githubtest.WorkflowRun{ID: 1, WorkflowID: 1, Status: "completed", CreatedAt: "2026-11-02T00:00:00Z"},
		githubtest.WorkflowRun{ID: 2, WorkflowID: 1, Status: "completed", CreatedAt: "2026-11-01T00:00:00Z"},
	)
	defer s.Close()

	var out, errb bytes.Buffer
	code := run(
		[]string{"runs", "-repo", "blanketops/environments-bots", "-api-url", s.URL, "-keep", "1", "-dry-run"},
		&out, &errb, env(nil),
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	if len(s.WorkflowRuns) != 2 {
		t.Fatalf("dry run deleted runs: %d left", len(s.WorkflowRuns))
	}
	if !strings.Contains(out.String(), "Would delete 1") {
		t.Fatalf("stdout = %q", out.String())
	}
}
