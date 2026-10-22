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

// env is a fake environment for run.
type env map[string]string

func (e env) get(k string) string { return e[k] }

func runCmd(t *testing.T, e env, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut, e.get)
	return code, out.String(), errOut.String()
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no command prints usage to stderr", args: nil, wantCode: 2, wantStderr: "Usage:"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "dev\n"},
		{name: "--version", args: []string{"--version"}, wantCode: 0, wantStdout: "dev\n"},
		{name: "-v", args: []string{"-v"}, wantCode: 0, wantStdout: "dev\n"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "--help", args: []string{"--help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "-h", args: []string{"-h"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "unknown command", args: []string{"nope"}, wantCode: 2, wantStderr: `unknown command "nope"`},
		{name: "sync -h", args: []string{"sync", "-h"}, wantCode: 0, wantStderr: "-project"},
		{name: "sync bad flag", args: []string{"sync", "-nope"}, wantCode: 2, wantStderr: "-nope"},
		{name: "sync extra argument", args: []string{"sync", "extra"}, wantCode: 2, wantStderr: `unexpected argument "extra"`},
		{name: "sync bad project", args: []string{"sync", "-project", "blanketops"}, wantCode: 2, wantStderr: "not org/number"},
		{name: "sync without token", args: []string{"sync", "-repo", "o/r"}, wantCode: 1, wantStderr: "no token"},
		{name: "sync bad repo", args: []string{"sync", "-dry-run", "-repo", "bad"}, wantCode: 1, wantStderr: "not owner/name"},
		{name: "report without inputs", args: []string{"report", "-repo", "o/r"}, wantCode: 2, wantStderr: "-go-json"},
		{name: "report bad flag", args: []string{"report", "-nope"}, wantCode: 2, wantStderr: "-nope"},
		{name: "report without token", args: []string{"report", "-repo", "o/r", "-go-json", "x"}, wantCode: 1, wantStderr: "no token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(t, env{}, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr %q)", code, tt.wantCode, stderr)
			}
			checkOutput(t, "stdout", stdout, tt.wantStdout)
			checkOutput(t, "stderr", stderr, tt.wantStderr)
		})
	}
}

// checkOutput fails when want is empty but got is not, or when got does not
// contain want. A want ending in a newline must match exactly.
func checkOutput(t *testing.T, stream, got, want string) {
	t.Helper()
	switch {
	case want == "":
		if got != "" {
			t.Errorf("%s = %q, want nothing", stream, got)
		}
	case strings.HasSuffix(want, "\n"):
		if got != want {
			t.Errorf("%s = %q, want %q", stream, got, want)
		}
	case !strings.Contains(got, want):
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}

func TestVersionIsOverridable(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "v0.0.1"
	if code, stdout, _ := runCmd(t, env{}, "version"); code != 0 || stdout != "v0.0.1\n" {
		t.Errorf("version: code %d, stdout %q", code, stdout)
	}
}

// repo builds a repository with one Ginkgo spec file and its suite file,
// matching the results fixture in internal/results/testdata, plus a
// tests/data file that must be ignored.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(p, content string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.27.0\n")
	write("tests/suite/suite_test.go", "package suite\n\nimport \"testing\"\n\nfunc TestSuite(t *testing.T) {}\n")
	write("tests/suite/specs_test.go", "package suite\n\nvar _ = Describe(\"Widget\", func() {})\n")
	write("tests/data/suite/fixture_test.go", "package suite\n\nimport \"testing\"\n\nfunc TestFixture(t *testing.T) {}\n")
	return root
}

const fixtures = "../../internal/results/testdata/"

func TestSyncAndReport(t *testing.T) {
	srv := githubtest.New("blanketops", "environments-tests", "tok")
	defer srv.Close()
	root := repo(t)
	summary := filepath.Join(t.TempDir(), "summary.md")
	e := env{
		"GH_TOKEN":            "tok",
		"GITHUB_REPOSITORY":   "blanketops/environments-tests",
		"GITHUB_API_URL":      srv.URL,
		"GITHUB_STEP_SUMMARY": summary,
		"GITHUB_SERVER_URL":   "https://github.com",
		"GITHUB_RUN_ID":       "99",
		"GITHUB_WORKFLOW":     "Suite Tests",
		"GITHUB_RUN_NUMBER":   "7",
		"GITHUB_SHA":          "abcdef0123456789",
	}

	code, stdout, stderr := runCmd(t, e, "sync", "-root", root, "-project", "blanketops/1", "-source", "PR #1")
	if code != 0 {
		t.Fatalf("sync: code %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "2 test files: 0 already tracked, 2 created.") {
		t.Errorf("sync stdout:\n%s", stdout)
	}
	for _, title := range []string{"[test] suite/suite_test", "[test] suite/specs_test"} {
		if srv.Issue(title) == nil {
			t.Errorf("no issue %q", title)
		}
	}
	if srv.Issue("[test] data/suite/fixture_test") != nil {
		t.Error("tests/data must not get an issue")
	}
	if len(srv.Items) != 2 {
		t.Errorf("project items = %d, want 2", len(srv.Items))
	}

	code, stdout, stderr = runCmd(t, e, "report", "-root", root, "-ref", "PR #53",
		"-go-json", fixtures+"go-test-ginkgo.json",
		"-ginkgo-json", fixtures+"ginkgo-report.json")
	if code != 0 {
		t.Fatalf("report: code %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "| `tests/suite/specs_test.go` | [#1]") || !strings.Contains(stdout, "| 1 | 1 | 2 |") {
		t.Errorf("report stdout:\n%s", stdout)
	}
	specs := srv.Issue("[test] suite/specs_test")
	if len(specs.Comments) != 1 {
		t.Fatalf("specs comments = %d, want 1", len(specs.Comments))
	}
	for _, want := range []string{
		"**Failed**: 1 passed, 1 failed, 2 skipped",
		"[Suite Tests #7](https://github.com/blanketops/environments-tests/actions/runs/99) on `abcdef012345` (`PR #53`)",
		"| `Widget / when built / fails` | failed |",
		"specs_test.go:11",
	} {
		if !strings.Contains(specs.Comments[0], want) {
			t.Errorf("specs comment lacks %q:\n%s", want, specs.Comments[0])
		}
	}
	if suite := srv.Issue("[test] suite/suite_test"); len(suite.Comments) != 1 || !strings.Contains(suite.Comments[0], "| `TestSuite` | failed |") {
		t.Errorf("suite comments = %v", suite.Comments)
	}

	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(data); !strings.Contains(s, "## Test tracking issues") || !strings.Contains(s, "## Test results posted to issues") {
		t.Errorf("summary file:\n%s", s)
	}
}

func TestDryRunNeedsNoToken(t *testing.T) {
	srv := githubtest.New("blanketops", "environments-tests", "")
	defer srv.Close()
	e := env{"GITHUB_REPOSITORY": "blanketops/environments-tests", "GITHUB_API_URL": srv.URL}
	code, stdout, stderr := runCmd(t, e, "sync", "-root", repo(t), "-dry-run")
	if code != 0 || !strings.Contains(stdout, "2 to create") || len(srv.Issues) != 0 {
		t.Errorf("dry run: code %d, stdout %q, stderr %q, issues %d", code, stdout, stderr, len(srv.Issues))
	}
}

func TestCommandFailures(t *testing.T) {
	srv := githubtest.New("blanketops", "environments-tests", "")
	defer srv.Close()
	e := env{"GITHUB_REPOSITORY": "blanketops/environments-tests", "GITHUB_API_URL": srv.URL, "GITHUB_TOKEN": "x"}
	root := repo(t)
	badSummary := filepath.Join(t.TempDir(), "no", "such", "dir")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"sync: no tests dir", []string{"sync", "-root", t.TempDir()}, "discover"},
		{"report: no tests dir", []string{"report", "-root", t.TempDir(), "-go-json", "x"}, "discover"},
		{"report: missing input", []string{"report", "-root", root, "-go-json", "missing.json"}, "missing.json"},
		{"report: broken Ginkgo report", []string{"report", "-root", root, "-ginkgo-json", fixtures + "go-test.json"}, "Ginkgo"},
		{"report: unwritable summary", []string{"report", "-root", root, "-go-json", fixtures + "go-test.json", "-summary", badSummary}, "no such file"},
		{"sync: unwritable summary", []string{"sync", "-root", root, "-dry-run", "-summary", badSummary}, "no such file"},
	}
	for _, c := range cases {
		code, _, stderr := runCmd(t, e, c.args...)
		if code != 1 || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: code %d, stderr %q; want 1 and %q", c.name, code, stderr, c.want)
		}
	}

	noMod := repo(t)
	if err := os.Remove(filepath.Join(noMod, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCmd(t, e, "report", "-root", noMod, "-go-json", "x"); code != 1 || !strings.Contains(stderr, "go.mod") {
		t.Errorf("no go.mod: code %d, stderr %q", code, stderr)
	}

	srv.FailPaths["GET /repos"] = 500
	if code, _, stderr := runCmd(t, e, "sync", "-root", root); code != 1 || !strings.Contains(stderr, "HTTP 500") {
		t.Errorf("sync API failure: code %d, stderr %q", code, stderr)
	}
	if code, _, stderr := runCmd(t, e, "report", "-root", root, "-go-json", fixtures+"go-test.json"); code != 1 || !strings.Contains(stderr, "HTTP 500") {
		t.Errorf("report API failure: code %d, stderr %q", code, stderr)
	}
	delete(srv.FailPaths, "GET /repos")

	srv.GraphQLError = "Resource not accessible by integration"
	if code, _, stderr := runCmd(t, e, "sync", "-root", root, "-project", "blanketops/1"); code != 0 || !strings.Contains(stderr, "warning") {
		t.Errorf("project warning: code %d, stderr %q; want 0 with a warning", code, stderr)
	}
}

func TestModulePath(t *testing.T) {
	dir := t.TempDir()
	for content, want := range map[string]string{
		"module example.com/m\n":           "example.com/m",
		"// c\nmodule \"example.com/q\"\n": "example.com/q",
		"\tmodule\texample.com/t\n":        "example.com/t",
	} {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := modulePath(dir); err != nil || got != want {
			t.Errorf("modulePath(%q) = %q, %v; want %q", content, got, err, want)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("modulex y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := modulePath(dir); err == nil {
		t.Error("want an error without a module line")
	}
}

func TestRunDefaults(t *testing.T) {
	if got := runURL(env{}.get); got != "" {
		t.Errorf("runURL without env = %q", got)
	}
	if got := runName(env{"GITHUB_WORKFLOW": "W"}.get); got != "W" {
		t.Errorf("runName without a number = %q", got)
	}
	var f files
	_ = f.Set("a")
	_ = f.Set("b")
	if f.String() != "a,b" {
		t.Errorf("files = %q", f.String())
	}
}
