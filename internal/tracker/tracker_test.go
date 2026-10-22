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

package tracker

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blanketops/environments-bots/internal/discover"
	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
	"github.com/blanketops/environments-bots/internal/results"
)

var (
	routeFile = discover.File{Path: "tests/fake/networks/route_test.go", Dir: "tests/fake/networks", Key: "fake/networks/route_test", Layer: "fake", Containers: []string{"Route Controller"}}
	buildFile = discover.File{Path: "tests/grpc/build_grpc_test.go", Dir: "tests/grpc", Key: "grpc/build_grpc_test", Layer: "grpc", Tests: []string{"TestA", "TestB"}}
	topFile   = discover.File{Path: "tests/top_test.go", Dir: "tests", Key: "top_test", Tests: []string{"TestTop"}}
)

func setup(t *testing.T) (*github.Client, *githubtest.Server) {
	t.Helper()
	srv := githubtest.New("blanketops", "environments-tests", "")
	t.Cleanup(srv.Close)
	c, err := github.New(srv.URL, "", "blanketops/environments-tests")
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestSync(t *testing.T) {
	c, srv := setup(t)
	ctx := context.Background()
	existing := srv.AddIssue(githubtest.Issue{Title: "[test] grpc/build_grpc_test", Labels: []string{Label}, State: "closed"})
	srv.AddIssue(githubtest.Issue{Title: "[test] grpc/build_grpc_test", Labels: []string{Label}})     // a later duplicate
	srv.AddIssue(githubtest.Issue{Title: "[test] fake/networks/route_test", Labels: []string{"bug"}}) // not tracking

	files := []discover.File{routeFile, buildFile, topFile}
	opts := SyncOptions{Files: files, Project: &ProjectRef{Org: "blanketops", Number: 1, Status: "Test"}, Source: "https://github.example/pr/9"}

	dry, err := Sync(ctx, c, SyncOptions{Files: files, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Created) != 2 || len(dry.Existing) != 1 || len(srv.Issues) != 3 {
		t.Fatalf("dry run: created %d, existing %d, issues %d; want 2, 1, 3 (nothing written)", len(dry.Created), len(dry.Existing), len(srv.Issues))
	}

	res, err := Sync(ctx, c, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Existing) != 1 || res.Existing[0].Issue.Number != existing {
		t.Errorf("existing = %+v, want the oldest #%d (closed, not duplicated)", res.Existing, existing)
	}
	if len(res.Created) != 2 || len(res.Warnings) != 0 {
		t.Fatalf("created %d, warnings %v; want 2 and none", len(res.Created), res.Warnings)
	}
	route := srv.Issue("[test] fake/networks/route_test")
	for _, is := range srv.Issues {
		if is.Title == "[test] fake/networks/route_test" && strings.Join(is.Labels, ",") == Label+",fake" {
			route = is
		}
	}
	if route == nil || strings.Join(route.Labels, ",") != Label+",fake" {
		t.Fatalf("route issue = %+v, want labels test-tracking,fake", route)
	}
	for _, want := range []string{"`tests/fake/networks/route_test.go`", "| Layer | `fake` |", "`Route Controller`", "https://github.example/pr/9"} {
		if !strings.Contains(route.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, route.Body)
		}
	}
	if top := srv.Issue("[test] top_test"); top == nil || strings.Join(top.Labels, ",") != Label {
		t.Errorf("top issue = %+v, want only the tracking label", top)
	}
	if !srv.Labels[Label] || !srv.Labels["fake"] || srv.Labels["grpc"] {
		t.Errorf("labels = %v, want test-tracking and fake (grpc only for new issues)", srv.Labels)
	}
	if len(srv.Items) != 2 || srv.Items[0].Option != "opt-test" {
		t.Errorf("project items = %+v, want both new issues in Test", srv.Items)
	}

	again, err := Sync(ctx, c, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Created) != 0 || len(srv.Items) != 2 {
		t.Errorf("second sync created %d, items %d; want 0 and 2", len(again.Created), len(srv.Items))
	}
}

func TestSyncProjectProblemsAreWarnings(t *testing.T) {
	c, srv := setup(t)
	ctx := context.Background()

	res, err := Sync(ctx, c, SyncOptions{Files: []discover.File{buildFile}, Project: &ProjectRef{Org: "blanketops", Number: 1, Status: "Nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 1 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], `"Nope"`) {
		t.Errorf("unknown status: %+v", res)
	}

	srv.GraphQLError = "Resource not accessible by integration"
	res, err = Sync(ctx, c, SyncOptions{Files: []discover.File{topFile}, Project: &ProjectRef{Org: "blanketops", Number: 1, Status: "Test"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 1 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not accessible") {
		t.Errorf("no project access: %+v", res)
	}
}

func TestSyncErrors(t *testing.T) {
	ctx := context.Background()
	for _, fail := range []string{
		"GET /repos/blanketops/environments-tests/issues",
		"POST /repos/blanketops/environments-tests/labels",
		"POST /repos/blanketops/environments-tests/issues",
	} {
		c, srv := setup(t)
		srv.FailPaths[fail] = http.StatusForbidden
		if _, err := Sync(ctx, c, SyncOptions{Files: []discover.File{buildFile}}); err == nil {
			t.Errorf("%s failing: want an error", fail)
		}
	}
}

func fileResults() []results.FileResults {
	return []results.FileResults{
		{File: buildFile, Results: []results.Result{
			{Name: "TestA", Outcome: results.Passed, Duration: 1500 * time.Millisecond},
			{Name: "TestB", Outcome: results.Failed, Duration: 20 * time.Millisecond, Output: "boom\n@someone ``` nasty"},
		}},
		{File: topFile, Results: []results.Result{{Name: "TestTop", Outcome: results.Skipped, Output: "not yet"}}},
	}
}

func TestReport(t *testing.T) {
	c, srv := setup(t)
	ctx := context.Background()
	n := srv.AddIssue(githubtest.Issue{Title: "[test] grpc/build_grpc_test", Labels: []string{Label}})
	run := RunInfo{URL: "https://github.example/runs/7", Name: "Suite Tests #7", SHA: "0123456789abcdef", Ref: "PR #53"}

	dry, err := Report(ctx, c, ReportOptions{Results: fileResults(), Run: run, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Posted) != 1 || len(srv.Issues[n-1].Comments) != 0 {
		t.Fatalf("dry run posted %+v, comments %v", dry.Posted, srv.Issues[n-1].Comments)
	}

	res, err := Report(ctx, c, ReportOptions{Results: fileResults(), Run: run})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Posted) != 1 || res.Posted[0].Passed != 1 || res.Posted[0].Failed != 1 {
		t.Errorf("posted = %+v", res.Posted)
	}
	if len(res.Untracked) != 1 || res.Untracked[0].Path != topFile.Path {
		t.Errorf("untracked = %+v, want the top file", res.Untracked)
	}
	comments := srv.Issues[n-1].Comments
	if len(comments) != 1 {
		t.Fatalf("comments = %v", comments)
	}
	body := comments[0]
	for _, want := range []string{
		"**Failed**: 1 passed, 1 failed, 0 skipped (1.5s)",
		"[Suite Tests #7](https://github.example/runs/7) on `0123456789ab` (`PR #53`)",
		"| `TestA` | passed | 1.5s |",
		"| `TestB` | failed | 20ms |",
		"<summary>`TestB` failed</summary>",
		"````text\nboom\n@someone ``` nasty\n````",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("comment lacks %q:\n%s", want, body)
		}
	}

	c2, srv2 := setup(t)
	srv2.AddIssue(githubtest.Issue{Title: "[test] grpc/build_grpc_test", Labels: []string{Label}})
	srv2.FailPaths["POST /repos/blanketops/environments-tests/issues/1/comments"] = http.StatusForbidden
	if _, err := Report(ctx, c2, ReportOptions{Results: fileResults(), Run: run}); err == nil || !strings.Contains(err.Error(), "#1") {
		t.Errorf("comment failure: err = %v", err)
	}
	srv2.FailPaths["GET /repos"] = http.StatusForbidden
	if _, err := Report(ctx, c2, ReportOptions{Results: fileResults(), Run: run}); err == nil {
		t.Error("list failure: want an error")
	}
}

func TestCode(t *testing.T) {
	for in, want := range map[string]string{
		"TestA":            "`TestA`",
		"a | b":            "`a \\| b`",
		"two\nlines  here": "`two lines here`",
		"has `tick`":       "`` has `tick` ``",
		"`edge":            "`` `edge ``",
		"":                 "` `",
		"@someone":         "`@someone`",
	} {
		if got := code(in); got != want {
			t.Errorf("code(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunCommentLimitsAndVerdicts(t *testing.T) {
	var many []results.Result
	for i := 0; i < 40; i++ {
		many = append(many, results.Result{Name: "TestBig", Outcome: results.Failed, Output: strings.Repeat("x", 3000)})
	}
	many = append(many, results.Result{Name: "TestSkipped", Outcome: results.Skipped, Output: "reason"})
	body := RunComment(results.FileResults{File: buildFile, Results: many}, RunInfo{})
	if len(body) > 65536 {
		t.Errorf("comment is %d characters, over GitHub's limit", len(body))
	}
	if !strings.Contains(body, "more tests is left out") || strings.Contains(body, "reason") {
		t.Errorf("want failures kept first and the rest cut with a note")
	}
	if !strings.Contains(body, "run\n\n|") {
		t.Errorf("a run with no URL or name should still label the line:\n%s", body[:200])
	}

	for _, c := range []struct {
		rs   []results.Result
		want string
	}{
		{[]results.Result{{Outcome: results.Passed}, {Outcome: results.Skipped}}, "**Passed**"},
		{[]results.Result{{Outcome: results.Skipped}}, "**Skipped**"},
		{nil, "**No results**"},
	} {
		if got := RunComment(results.FileResults{Results: c.rs}, RunInfo{Name: "x [y]"}); !strings.HasPrefix(got, c.want) || !strings.Contains(got, "x (y)") {
			t.Errorf("RunComment(%v) starts %q, want %q and a link label without brackets", c.rs, got[:20], c.want)
		}
	}
}

func TestSummaries(t *testing.T) {
	sync := SyncSummary(SyncResult{
		Existing: []Tracked{{File: buildFile, Issue: github.Issue{Number: 3, State: "open", HTMLURL: "https://x/3"}}},
		Created:  []Tracked{{File: topFile}},
		Warnings: []string{"project: no access"},
	}, true)
	for _, want := range []string{"Dry run", "2 test files: 1 already tracked, 1 to create.", "| `tests/top_test.go` | (new) | new |", "[#3](https://x/3) | open", "`project: no access`"} {
		if !strings.Contains(sync, want) {
			t.Errorf("sync summary lacks %q:\n%s", want, sync)
		}
	}
	if s := SyncSummary(SyncResult{Created: []Tracked{{File: topFile, Issue: github.Issue{Number: 4}}}}, false); !strings.Contains(s, "1 created") || !strings.Contains(s, "| #4 |") {
		t.Errorf("sync summary:\n%s", s)
	}

	rep := ReportSummary(ReportResult{
		Posted:    []Posted{{Tracked: Tracked{File: buildFile, Issue: github.Issue{Number: 3}}, Passed: 2, Failed: 1}},
		Untracked: []discover.File{topFile},
	}, false)
	for _, want := range []string{"| `tests/grpc/build_grpc_test.go` | #3 | 2 | 1 | 0 |", "- `tests/top_test.go`"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report summary lacks %q:\n%s", want, rep)
		}
	}
	if s := ReportSummary(ReportResult{}, true); !strings.Contains(s, "Dry run") || !strings.Contains(s, "No test file had results") {
		t.Errorf("empty report summary:\n%s", s)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "-", 300 * time.Microsecond: "<1ms", 5 * time.Millisecond: "5ms", 2500 * time.Millisecond: "2.5s"} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %q, want %q", d, got, want)
		}
	}
}
