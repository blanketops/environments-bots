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

package results

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/blanketops/environments-bots/internal/discover"
)

// The testdata files are real output of Go 1.27 and Ginkgo 2.31:
//   - go-test.json: `go test -json ./...` over package ok (TestPass with a
//     passing subtest, TestFail, TestSkip) and package broken, which does
//     not compile;
//   - go-test-ginkgo.json and ginkgo-report.json: one Ginkgo suite
//     (tests/suite) whose specs_test.go has a passing, a failing, a skipped
//     and a pending It, run with -ginkgo.json-report.
// Absolute paths in them were rewritten to /src.

const module = "example.com/m"

func open(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func names(rs []Result) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Name+":"+string(r.Outcome))
	}
	return out
}

func TestGoTestJSON(t *testing.T) {
	run := NewRun()
	if err := run.AddGoTestJSON(open(t, "go-test.json")); err != nil {
		t.Fatal(err)
	}
	files := []discover.File{
		{Path: "ok/a_test.go", Dir: "ok", Key: "ok/a_test", Tests: []string{"TestPass", "TestFail", "TestSkip"}},
		{Path: "broken/b_test.go", Dir: "broken", Key: "broken/b_test", Tests: []string{"TestX"}},
		{Path: "broken/c_test.go", Dir: "broken", Key: "broken/c_test", Tests: []string{"TestY"}},
		{Path: "idle/d_test.go", Dir: "idle", Key: "idle/d_test", Tests: []string{"TestZ"}},
	}
	got := run.Assign(files, module, "/src")

	if len(got) != 3 {
		t.Fatalf("got %d files, want 3 (idle has no results): %+v", len(got), got)
	}
	// Sorted by path: broken/b, broken/c, ok/a.
	for i, want := range []string{"broken/b_test.go", "broken/c_test.go", "ok/a_test.go"} {
		if got[i].File.Path != want {
			t.Errorf("file %d = %s, want %s", i, got[i].File.Path, want)
		}
	}
	for _, fr := range got[:2] {
		if len(fr.Results) != 1 || fr.Results[0].Name != "(build)" || fr.Results[0].Outcome != Failed {
			t.Fatalf("%s: want one failed (build) result, got %+v", fr.File.Path, fr.Results)
		}
		if !strings.Contains(fr.Results[0].Output, "undefined: undefinedThing") {
			t.Errorf("%s: build output = %q, want the compiler error", fr.File.Path, fr.Results[0].Output)
		}
	}

	ok := got[2]
	if want := []string{"TestFail:failed", "TestPass:passed", "TestSkip:skipped"}; strings.Join(names(ok.Results), ",") != strings.Join(want, ",") {
		t.Errorf("ok results = %v, want %v (subtests folded into their test)", names(ok.Results), want)
	}
	byName := map[string]Result{}
	for _, r := range ok.Results {
		byName[r.Name] = r
	}
	if out := byName["TestFail"].Output; !strings.Contains(out, "context line") || !strings.Contains(out, "boom") {
		t.Errorf("TestFail output = %q, want the log and the failure", out)
	}
	if out := byName["TestSkip"].Output; !strings.Contains(out, "not yet") {
		t.Errorf("TestSkip output = %q, want the skip reason", out)
	}
	if out := byName["TestPass"].Output; out != "" {
		t.Errorf("TestPass output = %q, want none for a pass", out)
	}
	if p, f, s := ok.Counts(); p != 1 || f != 1 || s != 1 {
		t.Errorf("Counts() = %d, %d, %d; want 1, 1, 1", p, f, s)
	}
}

func TestGinkgo(t *testing.T) {
	run := NewRun()
	if err := run.AddGoTestJSON(open(t, "go-test-ginkgo.json")); err != nil {
		t.Fatal(err)
	}
	if err := run.AddGinkgoJSON(open(t, "ginkgo-report.json")); err != nil {
		t.Fatal(err)
	}
	files := []discover.File{
		{Path: "tests/suite/suite_test.go", Dir: "tests/suite", Key: "suite/suite_test", Tests: []string{"TestSuite"}},
		{Path: "tests/suite/specs_test.go", Dir: "tests/suite", Key: "suite/specs_test", Containers: []string{"Widget"}},
	}

	for _, root := range []string{"/src", "/elsewhere"} { // the second matches on /tests/
		got := run.Assign(files, module, root)
		if len(got) != 2 {
			t.Fatalf("root %s: got %d files, want 2", root, len(got))
		}
		specs, suite := got[0], got[1]
		want := []string{
			"Widget / when built / fails:failed",
			"Widget / when built / is pending:skipped",
			"Widget / when built / passes:passed",
			"Widget / when built / skips:skipped",
		}
		if strings.Join(names(specs.Results), ",") != strings.Join(want, ",") {
			t.Errorf("root %s: specs = %v, want %v", root, names(specs.Results), want)
		}
		if w := []string{"TestSuite:failed"}; strings.Join(names(suite.Results), ",") != w[0] {
			t.Errorf("root %s: suite = %v, want %v", root, names(suite.Results), w)
		}
		for _, r := range specs.Results {
			switch r.Name {
			case "Widget / when built / fails":
				if !strings.HasPrefix(r.Output, "specs_test.go:11\n") || !strings.Contains(r.Output, "to equal") {
					t.Errorf("fails output = %q, want location and message", r.Output)
				}
				if r.Duration <= 0 {
					t.Errorf("fails duration = %v, want > 0", r.Duration)
				}
			case "Widget / when built / skips":
				if !strings.Contains(r.Output, "not wired yet") {
					t.Errorf("skips output = %q, want the skip reason", r.Output)
				}
			case "Widget / when built / is pending":
				if r.Output != "" {
					t.Errorf("pending output = %q, want none", r.Output)
				}
			}
		}
	}
}

func TestGinkgoOutcome(t *testing.T) {
	for state, want := range map[string]Outcome{
		"passed": Passed, "skipped": Skipped, "pending": Skipped,
		"failed": Failed, "panicked": Failed, "interrupted": Failed, "aborted": Failed, "timedout": Failed,
	} {
		if got := ginkgoOutcome(state); got != want {
			t.Errorf("ginkgoOutcome(%q) = %s, want %s", state, got, want)
		}
	}
}

func TestAddGoTestJSONIgnoresNoise(t *testing.T) {
	run := NewRun()
	in := strings.Join([]string{
		"go: downloading example.com/x v1.0.0",
		"",
		"{not json",
		`{"Action":"pass","Package":"example.com/m/p","Test":"TestA","Elapsed":1.5}`,
	}, "\n")
	if err := run.AddGoTestJSON(strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	got := run.Assign([]discover.File{{Path: "p/a_test.go", Dir: "p", Tests: []string{"TestA"}}}, module, "/src")
	if len(got) != 1 || got[0].Results[0].Duration.Seconds() != 1.5 {
		t.Errorf("got %+v, want TestA passed in 1.5s", got)
	}
}

func TestModuleRootPackage(t *testing.T) {
	run := NewRun()
	in := `{"Action":"fail","Package":"example.com/m","Test":"TestRoot","Elapsed":0}`
	if err := run.AddGoTestJSON(strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	got := run.Assign([]discover.File{{Path: "root_test.go", Dir: ".", Tests: []string{"TestRoot"}}}, module, "/src")
	if len(got) != 1 {
		t.Fatalf("got %+v, want the root package's file", got)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestReadErrors(t *testing.T) {
	if err := NewRun().AddGoTestJSON(errReader{}); err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("AddGoTestJSON err = %v", err)
	}
	if err := NewRun().AddGinkgoJSON(strings.NewReader("{")); err == nil {
		t.Error("AddGinkgoJSON accepted a broken report")
	}
}

func TestTail(t *testing.T) {
	if got := tail("short\n"); got != "short" {
		t.Errorf("tail(short) = %q", got)
	}
	long := strings.Repeat("x", maxOutput) + "\nlast line\n"
	got := tail(long)
	if !strings.HasPrefix(got, "...\n") || !strings.HasSuffix(got, "last line") || len(got) > maxOutput+4 {
		t.Errorf("tail(long) = %d bytes, starts %q, ends %q", len(got), got[:10], got[len(got)-10:])
	}
	// The cut lands on a line boundary when there is one.
	lines := strings.Repeat("0123456789\n", maxOutput/5)
	if got := tail(lines); !strings.HasPrefix(got, "...\n0123456789") {
		t.Errorf("tail(lines) starts %q, want a whole line", got[:20])
	}
}

func TestRelPath(t *testing.T) {
	for _, c := range []struct{ name, root, want string }{
		{"/src/tests/a/x_test.go", "/src", "tests/a/x_test.go"},
		{"/src/tests/a/x_test.go", "/src/", "tests/a/x_test.go"},
		{"/runner/work/tests/a/x_test.go", "/src", "tests/a/x_test.go"},
		{"/other/x_test.go", "/src", "/other/x_test.go"},
	} {
		if got := relPath(c.name, c.root); got != c.want {
			t.Errorf("relPath(%q, %q) = %q, want %q", c.name, c.root, got, c.want)
		}
	}
}
