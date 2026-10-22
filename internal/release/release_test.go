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

package release

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
	"github.com/blanketops/environments-bots/internal/semver"
)

var board = &ProjectRef{Org: "blanketops", Number: 1}

func setup(t *testing.T) (*github.Client, *githubtest.Server) {
	t.Helper()
	srv := githubtest.New("blanketops", "environments", "")
	srv.StatusOptions = map[string]string{"Backlog": "opt-backlog", "Release": "opt-release", "Done": "opt-done"}
	t.Cleanup(srv.Close)
	c, err := github.New(srv.URL, "", "blanketops/environments")
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

// environments as it is on the board today: v0.8.7 released, issues for
// v0.8.8 and v0.8.9, and two for v0.0.3.
func environments(srv *githubtest.Server) (v088, v089, dup1, dup2 int) {
	srv.Tags = []githubtest.Tag{{Name: "v0.8.7", SHA: "sha087"}, {Name: "v0.8.6", SHA: "sha086"}, {Name: "nightly", SHA: "x"}}
	// Opened by people, as on the board (go-live planning).
	v088 = srv.AddIssue(githubtest.Issue{Title: "Release: v0.8.8", Labels: []string{Label}, Author: "ntlaletsi70"})
	v089 = srv.AddIssue(githubtest.Issue{Title: "Release: v0.8.9", Labels: []string{Label}, Author: "ntlaletsi70"})
	dup1 = srv.AddIssue(githubtest.Issue{Title: "Release: v0.0.3", Labels: []string{Label}, Author: "ntlaletsi70"})
	dup2 = srv.AddIssue(githubtest.Issue{Title: "Release: v0.0.3", Labels: []string{Label}, Author: "ntlaletsi70"})
	srv.AddIssue(githubtest.Issue{Title: "Release: v0.8.7", Labels: []string{Label}, State: "closed"})
	srv.AddIssue(githubtest.Issue{Title: "Release: v0.8.8", PR: true}) // a pull request, not an issue
	srv.AddIssue(githubtest.Issue{Title: "Release: next", Labels: []string{Label}})
	srv.Compares["v0.8.7...develop"] = []githubtest.Commit{
		{SHA: "aaaaaaa111", Message: "fix(api): handle empty spec"},
		{SHA: "bbbbbbb222", Message: "Merge pull request #5 from blanketops/fix/x"},
		{SHA: "ccccccc333", Message: "feat: add a flag"},
		{SHA: "ddddddd444", Message: "Update README.md"},
	}
	return v088, v089, dup1, dup2
}

func checkOf(t *testing.T, r *Result, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", name, r.Checks)
	return Check{}
}

func issue(srv *githubtest.Server, n int) *githubtest.Issue {
	for _, is := range srv.Issues {
		if is.Number == n {
			return is
		}
	}
	return nil
}

func TestPrepareReusesIssueAndWarnsAboutStaleOnes(t *testing.T) {
	c, srv := setup(t)
	v088, _, dup1, dup2 := environments(srv)
	r, err := Prepare(context.Background(), c, Options{Version: "v0.8.8", Ref: "develop", Project: board, RunURL: "https://run/1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Failed() {
		t.Fatalf("checks failed: %+v", r.Checks)
	}
	if r.Issue.Number != v088 || r.IssueCreated || r.Previous.String() != "v0.8.7" {
		t.Errorf("issue %d created %v previous %v; want #%d reused after v0.8.7", r.Issue.Number, r.IssueCreated, r.Previous, v088)
	}
	if len(r.Commits) != 3 || r.Required != semver.Patch {
		t.Errorf("commits %d, required %v; want 3 (merge dropped) and patch (feat is patch before 1.0)", len(r.Commits), r.Required)
	}
	warn := strings.Join(r.Warnings, "\n")
	if len(r.Warnings) != 1 || !strings.Contains(warn, "ddddddd") {
		t.Errorf("people's Release issues must not be flagged; warnings:\n%s", warn)
	}
	// The releaser's own stale issues are.
	issue(srv, dup1).Author = DefaultBot
	srv.Issue("Release: next").Author = DefaultBot
	r, err = Prepare(context.Background(), c, Options{Version: "v0.8.8", Ref: "develop", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	warn = strings.Join(r.Warnings, "\n")
	for _, want := range []string{fmt.Sprintf("#%d (v0.0.3)", dup1), "names no release version"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warnings lack %q:\n%s", want, warn)
		}
	}
	if strings.Contains(warn, fmt.Sprintf("#%d ", dup2)) || strings.Contains(warn, "v0.8.9") {
		t.Errorf("flagged a person's issue:\n%s", warn)
	}
	got := issue(srv, v088).Comments
	if len(got) != 1 || !strings.Contains(got[0], "v0.8.8 prepare: passed") || !strings.Contains(got[0], "[Workflow run](https://run/1)") ||
		!strings.Contains(got[0], "Not Conventional Commits") {
		t.Errorf("comment = %q", got)
	}
	if len(srv.Items) != 1 || srv.Items[0].Option != "opt-release" {
		t.Errorf("board items = %+v, want the issue in Release", srv.Items)
	}
}

func TestPrepareRejectsSkippedVersionAndCommentsOnItsIssue(t *testing.T) {
	c, srv := setup(t)
	_, v089, _, _ := environments(srv)
	r, err := Prepare(context.Background(), c, Options{Version: "v0.8.9", Ref: "develop"})
	if err != nil {
		t.Fatal(err)
	}
	if v := checkOf(t, r, "Version"); v.OK || !strings.Contains(v.Detail, "v0.8.8, v0.9.0 or v1.0.0") {
		t.Errorf("version check = %+v", v)
	}
	for _, c := range r.Checks {
		if c.Name == "Semantic version" {
			t.Error("no semantic-version check for an invalid version")
		}
	}
	got := issue(srv, v089).Comments
	if len(got) != 1 || !strings.Contains(got[0], "failed") {
		t.Errorf("comment on #%d = %q, want the failure", v089, got)
	}
}

func TestPrepareRefusesDuplicatesAndOpensNothing(t *testing.T) {
	c, srv := setup(t)
	_, _, dup1, dup2 := environments(srv)
	before := len(srv.Issues)
	r, err := Prepare(context.Background(), c, Options{Version: "v0.0.3", Ref: "develop"})
	if err != nil {
		t.Fatal(err)
	}
	if ri := checkOf(t, r, "Release issue"); ri.OK || !strings.Contains(ri.Detail, fmt.Sprintf("#%d, #%d", dup1, dup2)) {
		t.Errorf("release issue check = %+v", ri)
	}
	if len(srv.Issues) != before || len(issue(srv, dup1).Comments)+len(issue(srv, dup2).Comments) != 0 {
		t.Error("duplicates must be left untouched")
	}
}

func TestPrepareFirstReleaseOpensIssue(t *testing.T) {
	c, srv := setup(t)
	srv.RefCommits["develop"] = []githubtest.Commit{
		{SHA: "3333333", Message: "fix(test-helper): count only TestXxx"},
		{SHA: "2222222", Message: "feat(test-helper): add the entry point"},
		{SHA: "1111111", Message: "chore: start"},
	}
	srv.AddMilestone("Milestone 1: Continuous Coverage & Static Analysis Gates")
	done := srv.AddMilestone("v0.0.1")
	srv.AddIssue(githubtest.Issue{Title: "shipped", Milestone: done, State: "closed"})
	r, err := Prepare(context.Background(), c, Options{Version: "v0.0.1", Ref: "develop", Project: board})
	if err != nil {
		t.Fatal(err)
	}
	if ms := checkOf(t, r, "Milestone"); !ms.OK || ms.Detail != "every item of milestone `v0.0.1` is closed" {
		t.Errorf("milestone = %+v", ms)
	}
	if r.Failed() || !r.IssueCreated || r.Previous != nil {
		t.Fatalf("result = %+v", r)
	}
	is := srv.Issue("Release: v0.0.1")
	if is == nil || strings.Join(is.Labels, ",") != Label || !srv.Labels[Label] || !strings.Contains(is.Body, "Tracks release `v0.0.1`, cut from `develop`") {
		t.Fatalf("issue = %+v, labels %v", is, srv.Labels)
	}
	if r.Commits[0].SHA != "1111111" {
		t.Errorf("commits not oldest first: %+v", r.Commits)
	}
	if !strings.Contains(is.Comments[0], "first release") {
		t.Errorf("comment = %q", is.Comments[0])
	}
}

func TestPrepareSemanticVersion(t *testing.T) {
	cases := []struct {
		prev, version string
		msgs          []string
		ok            bool
		detail        string
	}{
		{"v1.2.3", "v1.2.4", []string{"fix: a", "feat(x): b"}, false, "need a minor release (v1.3.0), not v1.2.4: `2222222` `feat(x): b`"},
		{"v1.2.3", "v1.3.0", []string{"fix: a", "feat(x): b"}, true, "a minor release; the commits need at least minor"},
		{"v1.2.3", "v1.3.0", []string{"fix!: a"}, false, "need a major release (v2.0.0)"},
		{"v0.4.1", "v0.4.2", []string{"refactor!: a"}, false, "need a minor release (v0.5.0)"},
		{"v0.4.1", "v0.5.0", []string{"fix: a"}, true, "a minor release; the commits need at least patch"},
	}
	for _, tc := range cases {
		c, srv := setup(t)
		srv.Tags = []githubtest.Tag{{Name: tc.prev, SHA: "p"}}
		var cs []githubtest.Commit
		for i, m := range tc.msgs {
			cs = append(cs, githubtest.Commit{SHA: strings.Repeat(fmt.Sprint(i+1), 8), Message: m})
		}
		srv.Compares[tc.prev+"...develop"] = cs
		r, err := Prepare(context.Background(), c, Options{Version: tc.version, Ref: "develop", DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		sv := checkOf(t, r, "Semantic version")
		if sv.OK != tc.ok || !strings.Contains(sv.Detail, tc.detail) {
			t.Errorf("%s -> %s %q: %+v, want ok=%v %q", tc.prev, tc.version, tc.msgs, sv, tc.ok, tc.detail)
		}
	}
}

func TestPrepareOtherFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("already tagged", func(t *testing.T) {
		c, srv := setup(t)
		srv.Tags = []githubtest.Tag{{Name: "v0.1.0"}, {Name: "v0.0.9"}}
		srv.Compares["v0.0.9...develop"] = []githubtest.Commit{{SHA: "1", Message: "fix: a"}}
		r, err := Prepare(ctx, c, Options{Version: "v0.1.0", Ref: "develop"})
		if err != nil {
			t.Fatal(err)
		}
		if v := checkOf(t, r, "Version"); v.OK || !strings.Contains(v.Detail, "already tagged") {
			t.Errorf("version = %+v", v)
		}
		if srv.Issue("Release: v0.1.0") != nil {
			t.Error("opened an issue for a tagged version")
		}
	})
	t.Run("closed issue, no changes, open milestone item", func(t *testing.T) {
		c, srv := setup(t)
		srv.Tags = []githubtest.Tag{{Name: "v0.1.0"}}
		srv.Compares["v0.1.0...develop"] = nil
		closed := srv.AddIssue(githubtest.Issue{Title: "Release: v0.1.1", Labels: []string{Label}, State: "closed"})
		m := srv.AddMilestone("0.1.1")
		srv.AddIssue(githubtest.Issue{Title: "gap", Milestone: m})
		srv.AddIssue(githubtest.Issue{Title: "done", Milestone: m, State: "closed"})
		r, err := Prepare(ctx, c, Options{Version: "v0.1.1", Ref: "develop"})
		if err != nil {
			t.Fatal(err)
		}
		if ri := checkOf(t, r, "Release issue"); ri.OK || !strings.Contains(ri.Detail, fmt.Sprintf("#%d is closed", closed)) {
			t.Errorf("release issue = %+v", ri)
		}
		if ch := checkOf(t, r, "Changes"); ch.OK {
			t.Errorf("changes = %+v", ch)
		}
		if ms := checkOf(t, r, "Milestone"); ms.OK || !strings.Contains(ms.Detail, "1 item(s) of milestone `0.1.1` still open: #2") {
			t.Errorf("milestone = %+v", ms)
		}
		if len(issue(srv, closed).Comments) != 0 {
			t.Error("commented on a closed issue")
		}
	})
	t.Run("dry run writes nothing", func(t *testing.T) {
		c, srv := setup(t)
		srv.RefCommits["develop"] = []githubtest.Commit{{SHA: "1", Message: "feat: a"}}
		r, err := Prepare(ctx, c, Options{Version: "v0.0.1", Ref: "develop", Project: board, DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		if r.Failed() || len(srv.Issues) != 0 || len(srv.Items) != 0 || !strings.Contains(checkOf(t, r, "Release issue").Detail, "would open") {
			t.Errorf("dry run: %+v, issues %d", r.Checks, len(srv.Issues))
		}
		if s := Summary(r); !strings.Contains(s, "Dry run") {
			t.Errorf("summary = %q", s)
		}
	})
	t.Run("usage and API errors", func(t *testing.T) {
		c, srv := setup(t)
		if _, err := Prepare(ctx, c, Options{Version: "0.1", Ref: "develop"}); err == nil {
			t.Error("bad version accepted")
		}
		if _, err := Prepare(ctx, c, Options{Version: "v0.0.1"}); err == nil {
			t.Error("missing ref accepted")
		}
		for _, path := range []string{"GET /repos/blanketops/environments/tags", "GET /repos/blanketops/environments/commits",
			"GET /repos/blanketops/environments/issues", "GET /repos/blanketops/environments/labels", "POST /repos/blanketops/environments/issues",
			"GET /repos/blanketops/environments/milestones", "POST /repos/blanketops/environments/issues/1/comments"} {
			srv.Issues, srv.Labels, srv.Milestones = nil, map[string]bool{}, nil
			srv.RefCommits["develop"] = []githubtest.Commit{{SHA: "1", Message: "feat: a"}}
			srv.FailPaths = map[string]int{path: http.StatusInternalServerError}
			if _, err := Prepare(ctx, c, Options{Version: "v0.0.1", Ref: "develop"}); err == nil {
				t.Errorf("failure of %s not reported", path)
			}
		}
	})
	t.Run("board failure is a warning", func(t *testing.T) {
		c, srv := setup(t)
		srv.RefCommits["develop"] = []githubtest.Commit{{SHA: "1", Message: "feat: a"}}
		srv.GraphQLError = "Resource not accessible by integration"
		r, err := Prepare(ctx, c, Options{Version: "v0.0.1", Ref: "develop", Project: board})
		if err != nil || r.Failed() {
			t.Fatalf("err %v, checks %+v", err, r.Checks)
		}
		if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "to Release on project blanketops/1") {
			t.Errorf("warnings = %q", r.Warnings)
		}
	})
}

func TestVerify(t *testing.T) {
	ctx := context.Background()
	c, srv := setup(t)
	srv.Tags = []githubtest.Tag{{Name: "v0.8.7"}}
	n := srv.AddIssue(githubtest.Issue{Title: "Release: v0.8.8", Labels: []string{Label}})
	srv.Pulls = []githubtest.Pull{
		{Number: 40, State: "open", Base: "release/v0.8.8", Head: "fix/late"},
		{Number: 41, State: "open", Base: "main", Head: "release/v0.8.8"},
	}
	r, err := Verify(ctx, c, Options{Version: "v0.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	if rb := checkOf(t, r, "Release branch"); rb.OK || !strings.Contains(rb.Detail, "#40") {
		t.Errorf("release branch = %+v", rb)
	}
	if r.ReleasePR == nil || r.ReleasePR.Number != 41 || r.Issue.Number != n || len(issue(srv, n).Comments) != 1 {
		t.Errorf("release PR %+v, issue %d, comments %d", r.ReleasePR, r.Issue.Number, len(issue(srv, n).Comments))
	}

	srv.Pulls = nil
	r, err = Verify(ctx, c, Options{Version: "v0.8.8"})
	if err != nil || r.Failed() {
		t.Fatalf("err %v, checks %+v", err, r.Checks)
	}
	if !strings.Contains(strings.Join(r.Warnings, ""), "finalize opens one") || !strings.Contains(Comment(r), "Ready to finalize") {
		t.Errorf("warnings %q, comment %q", r.Warnings, Comment(r))
	}

	srv.Tags = append(srv.Tags, githubtest.Tag{Name: "v0.8.8"})
	issue(srv, n).State = "closed"
	r, err = Verify(ctx, c, Options{Version: "v0.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	if checkOf(t, r, "Version").OK || checkOf(t, r, "Release issue").OK {
		t.Errorf("tagged version and closed issue must fail: %+v", r.Checks)
	}

	srv.AddIssue(githubtest.Issue{Title: "Release: v0.8.8", Labels: []string{Label}})
	r, _ = Verify(ctx, c, Options{Version: "v0.8.8"})
	if ri := checkOf(t, r, "Release issue"); ri.OK || !strings.Contains(ri.Detail, "duplicates") {
		t.Errorf("release issue = %+v", ri)
	}
	r, _ = Verify(ctx, c, Options{Version: "v0.9.0"})
	if ri := checkOf(t, r, "Release issue"); ri.OK || !strings.Contains(ri.Detail, "run prepare first") {
		t.Errorf("release issue = %+v", ri)
	}

	if _, err := Verify(ctx, c, Options{Version: "x"}); err == nil {
		t.Error("bad version accepted")
	}
	m := srv.AddMilestone("v0.9.0")
	for _, path := range []string{"GET /repos/blanketops/environments/git/ref", "GET /repos/blanketops/environments/issues",
		"GET /repos/blanketops/environments/pulls", "GET /repos/blanketops/environments/pulls?base=main",
		"GET /repos/blanketops/environments/milestones", fmt.Sprintf("GET /repos/blanketops/environments/issues?milestone=%d", m)} {
		srv.FailPaths = map[string]int{path: http.StatusBadGateway}
		if _, err := Verify(ctx, c, Options{Version: "v0.9.0"}); err == nil {
			t.Errorf("failure of %s not reported", path)
		}
	}
}

func TestComplete(t *testing.T) {
	ctx := context.Background()
	c, srv := setup(t)
	srv.Tags = []githubtest.Tag{{Name: "v0.8.8", SHA: "abcdef0123"}, {Name: "v0.8.7"}}
	n := srv.AddIssue(githubtest.Issue{Title: "Release: v0.8.8", Labels: []string{Label}})
	m := srv.AddMilestone("v0.8.8")
	issue(srv, n).Milestone = m
	srv.Pulls = []githubtest.Pull{{Number: 41, State: "closed", Base: "main", Head: "release/v0.8.8", Merged: true}}

	// Not released yet: the issue stays open with what is missing.
	r, err := Complete(ctx, c, Options{Version: "v0.8.8", Project: board})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Failed() || issue(srv, n).State != "open" || len(srv.Items) != 0 {
		t.Fatalf("missing release: failed %v, state %s, items %v", r.Failed(), issue(srv, n).State, srv.Items)
	}
	if got := issue(srv, n).Comments; len(got) != 1 || !strings.Contains(got[0], "no published release for v0.8.8") {
		t.Errorf("comment = %q", got)
	}

	srv.Releases["v0.8.8"] = githubtest.Release{Name: "v0.8.8"}
	r, err = Complete(ctx, c, Options{Version: "v0.8.8", Project: board, RunURL: "https://run/2"})
	if err != nil || r.Failed() {
		t.Fatalf("err %v, checks %+v", err, r.Checks)
	}
	is := issue(srv, n)
	last := is.Comments[len(is.Comments)-1]
	for _, want := range []string{"v0.8.8 complete: passed", "Released; this issue is closed", "releases/tag/v0.8.8", "`abcdef0`", "#41"} {
		if !strings.Contains(last, want) {
			t.Errorf("comment lacks %q:\n%s", want, last)
		}
	}
	if is.State != "closed" || len(srv.Items) != 1 || srv.Items[0].Option != "opt-done" || srv.Milestones[0].State != "closed" {
		t.Errorf("state %s, items %+v, milestone %s", is.State, srv.Items, srv.Milestones[0].State)
	}

	// Rerun on the closed issue: comments again, no second close.
	before := len(srv.Requests)
	if r, err = Complete(ctx, c, Options{Version: "v0.8.8"}); err != nil || r.Failed() {
		t.Fatalf("rerun: %v %+v", err, r.Checks)
	}
	for _, req := range srv.Requests[before:] {
		if strings.HasPrefix(req, "PATCH") {
			t.Errorf("rerun patched %s", req)
		}
	}
}

func TestCompleteOpensMissingIssueAndWarns(t *testing.T) {
	ctx := context.Background()
	c, srv := setup(t)
	srv.Tags = []githubtest.Tag{{Name: "v0.0.1", SHA: "1234567890"}}
	srv.Releases["v0.0.1"] = githubtest.Release{Prerelease: true}
	m := srv.AddMilestone("0.0.1")
	srv.AddIssue(githubtest.Issue{Title: "late", Milestone: m})
	r, err := Complete(ctx, c, Options{Version: "v0.0.1"})
	if err != nil || r.Failed() {
		t.Fatalf("err %v, checks %+v", err, r.Checks)
	}
	is := srv.Issue("Release: v0.0.1")
	if is == nil || is.State != "closed" || !r.IssueCreated {
		t.Fatalf("issue = %+v", is)
	}
	warn := strings.Join(r.Warnings, "\n")
	for _, want := range []string{"pre-release", "no merged pull request", "left open"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warnings lack %q:\n%s", want, warn)
		}
	}
	if srv.Milestones[0].State != "open" {
		t.Error("a milestone with open items must stay open")
	}
	if s := Summary(r); !strings.Contains(s, "(opened by this run)") {
		t.Errorf("summary = %q", s)
	}
}

func TestCompleteEdgeCases(t *testing.T) {
	ctx := context.Background()
	c, srv := setup(t)
	srv.AddIssue(githubtest.Issue{Title: "Release: v0.0.2", Labels: []string{Label}})
	srv.AddIssue(githubtest.Issue{Title: "Release: v0.0.2", Labels: []string{Label}})
	r, err := Complete(ctx, c, Options{Version: "v0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	if checkOf(t, r, "Tag").OK || checkOf(t, r, "Release issue").OK || r.Issue.Number != 0 {
		t.Errorf("checks %+v", r.Checks)
	}
	if s := Summary(r); !strings.Contains(s, "No Release issue was commented") {
		t.Errorf("summary = %q", s)
	}
	srv.Issues = nil
	srv.Tags = []githubtest.Tag{{Name: "v0.0.2", SHA: "1"}}
	srv.Releases["v0.0.2"] = githubtest.Release{}
	if _, err = Complete(ctx, c, Options{Version: "v0.0.2", DryRun: true}); err != nil || len(srv.Issues) != 0 {
		t.Errorf("dry run wrote: %v %d", err, len(srv.Issues))
	}
	if _, err := Complete(ctx, c, Options{Version: "v2"}); err == nil {
		t.Error("bad version accepted")
	}
	for _, path := range []string{"GET /repos/blanketops/environments/git/ref", "GET /repos/blanketops/environments/releases",
		"GET /repos/blanketops/environments/pulls", "GET /repos/blanketops/environments/issues", "GET /repos/blanketops/environments/labels",
		"POST /repos/blanketops/environments/issues", "GET /repos/blanketops/environments/milestones",
		"POST /repos/blanketops/environments/issues/1/comments", "PATCH /repos/blanketops/environments/issues/1"} {
		srv.Issues, srv.Labels = nil, map[string]bool{}
		srv.FailPaths = map[string]int{path: http.StatusInternalServerError}
		if _, err := Complete(ctx, c, Options{Version: "v0.0.2"}); err == nil {
			t.Errorf("failure of %s not reported", path)
		}
	}
	// A milestone that cannot be closed is a warning.
	srv.FailPaths = map[string]int{"PATCH /repos/blanketops/environments/milestones": 403}
	srv.Issues = nil
	srv.AddMilestone("v0.0.2")
	r, err = Complete(ctx, c, Options{Version: "v0.0.2"})
	if err != nil || !strings.Contains(strings.Join(r.Warnings, ""), "could not close milestone") {
		t.Errorf("err %v, warnings %q", err, r.Warnings)
	}
}

func TestRender(t *testing.T) {
	if got := code("a | b `c`"); got != "`` a \\| b `c` ``" {
		t.Errorf("code = %q", got)
	}
	if code("  ") != "` `" || code("`x`") != "`` `x` ``" {
		t.Error("code edge cases")
	}
	if upperFirst("") != "" {
		t.Error("upperFirst")
	}
	var cs []semver.Commit
	for i := 0; i < maxCommits+5; i++ {
		cs = append(cs, semver.ParseCommit(fmt.Sprint(i), "fix: x"))
	}
	if l := commitList(cs); !strings.Contains(l, "- and 5 more") {
		t.Errorf("commit list not capped:\n%s", l[len(l)-200:])
	}
	r := &Result{Stage: StageComplete, Version: semver.Version{Patch: 1}, Release: &github.Release{HTMLURL: "u"}, TagSHA: "abc"}
	if s := Comment(r); !strings.Contains(s, "| - |") || !strings.Contains(s, "\n\nReleased.\n") {
		t.Errorf("comment = %q", s)
	}
}
