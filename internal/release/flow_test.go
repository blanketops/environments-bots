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
)

func TestPlanNothingToRelease(t *testing.T) {
	c, srv := setup(t)
	srv.Tags = []githubtest.Tag{{Name: "v0.8.7"}}
	srv.Compares["v0.8.7...develop"] = []githubtest.Commit{{SHA: "m1", Message: "Merge branch 'main' into develop"}}
	r, err := Plan(context.Background(), c, Options{Ref: "develop", Project: board})
	if err != nil || r.Failed() || len(srv.Issues) != 0 {
		t.Fatalf("err %v, result %+v, issues %d", err, r, len(srv.Issues))
	}
	if !strings.Contains(r.Note, "nothing to release: develop has no commits since v0.8.7") || !strings.Contains(Summary(r), "### releaser plan") {
		t.Errorf("note %q\n%s", r.Note, Summary(r))
	}
}

func TestPlanOpensTheNextRelease(t *testing.T) {
	c, srv := setup(t)
	srv.Tags = []githubtest.Tag{{Name: "v0.8.7"}}
	srv.AddIssue(githubtest.Issue{Title: "Release: v0.8.7", Labels: []string{Label}, State: "closed"})
	srv.Compares["v0.8.7...develop"] = []githubtest.Commit{{SHA: "aaaaaaaa", Message: "fix: a"}, {SHA: "bbbbbbbb", Message: "feat: b"}}
	r, err := Plan(context.Background(), c, Options{Ref: "develop", Project: board, RunURL: "https://run/3"})
	if err != nil || r.Failed() {
		t.Fatalf("err %v, checks %+v", err, r.Checks)
	}
	is := srv.Issue("Release: v0.8.8")
	if is == nil || !r.IssueCreated || len(srv.Items) != 1 || srv.Items[0].Option != "opt-release" {
		t.Fatalf("issue %+v, items %+v", is, srv.Items)
	}
	for _, want := range []string{"v0.8.8 plan: passed", "`develop` has changes no release carries yet: v0.8.8 is planned. Run Create Release to cut `release/v0.8.8`",
		"v0.8.8, the patch release the commits need after v0.8.7", "Features:", "[Workflow run](https://run/3)"} {
		if !strings.Contains(is.Comments[0], want) {
			t.Errorf("comment lacks %q:\n%s", want, is.Comments[0])
		}
	}

	// A second push finds it planned and stays quiet.
	r, err = Plan(context.Background(), c, Options{Ref: "develop", Project: board})
	if err != nil || r.Issue.Number != is.Number || len(is.Comments) != 1 || !strings.Contains(r.Note, "plans v0.8.8, which covers the 2 commit(s)") {
		t.Errorf("second plan: %v %+v, comments %d", err, r, len(is.Comments))
	}
}

func TestPlanFirstReleaseAndDryRun(t *testing.T) {
	c, srv := setup(t)
	srv.RefCommits["develop"] = []githubtest.Commit{{SHA: "2", Message: "feat: b"}, {SHA: "1", Message: "chore: a"}}
	r, err := Plan(context.Background(), c, Options{Ref: "develop", DryRun: true})
	if err != nil || r.Version.String() != "v0.0.1" || len(srv.Issues) != 0 || !strings.Contains(checkOf(t, r, "Release issue").Detail, "would open") {
		t.Fatalf("dry run: %v %+v", err, r)
	}
	if r, err = Plan(context.Background(), c, Options{Ref: "develop"}); err != nil || !strings.Contains(checkOf(t, r, "Version").Detail, "v0.0.1, the first release") {
		t.Fatalf("plan: %v %+v", err, r)
	}
	if srv.Issue("Release: v0.0.1") == nil {
		t.Error("no issue opened")
	}
}

func TestPlanWithPlannedReleases(t *testing.T) {
	c, srv := setup(t)
	v088, v089, _, _ := environments(srv)
	r, err := Plan(context.Background(), c, Options{Ref: "develop"})
	if err != nil || r.Issue.Number != v088 || r.Failed() {
		t.Fatalf("err %v, issue %d, checks %+v", err, r.Issue.Number, r.Checks)
	}
	if w := strings.Join(r.Warnings, "\n"); !strings.Contains(w, fmt.Sprintf("#%d (v0.8.9) is also planned; only v0.8.8 is released next", v089)) {
		t.Errorf("warnings:\n%s", w)
	}
	srv.Branches["release/v0.8.8"] = "cut"
	if r, err = Plan(context.Background(), c, Options{Ref: "develop"}); err != nil || !strings.Contains(r.Note, "v0.8.8 is in progress on release/v0.8.8") {
		t.Errorf("in progress: %v %q", err, r.Note)
	}
	for _, is := range srv.Issues {
		if len(is.Comments) != 0 {
			t.Errorf("#%d commented by a quiet plan", is.Number)
		}
	}
}

func TestPlanRenamesWhenTheCommitsNeedMore(t *testing.T) {
	c, srv := setup(t)
	srv.Tags = []githubtest.Tag{{Name: "v0.4.1"}}
	n := srv.AddIssue(githubtest.Issue{Title: "Release: v0.4.2", Labels: []string{Label}})
	srv.Compares["v0.4.1...develop"] = []githubtest.Commit{{SHA: "11111111", Message: "fix: a"}, {SHA: "22222222", Message: "feat(api)!: drop v1"}}

	r, err := Plan(context.Background(), c, Options{Ref: "develop", DryRun: true})
	if err != nil || issue(srv, n).Title != "Release: v0.4.2" || r.Version.String() != "v0.5.0" {
		t.Fatalf("dry run renamed or misplanned: %v %+v", err, r)
	}
	r, err = Plan(context.Background(), c, Options{Ref: "develop"})
	if err != nil || r.Failed() {
		t.Fatalf("err %v, checks %+v", err, r.Checks)
	}
	is := issue(srv, n)
	if is.Title != "Release: v0.5.0" || r.RenamedFrom.String() != "v0.4.2" {
		t.Fatalf("title %q, renamed from %v", is.Title, r.RenamedFrom)
	}
	for _, want := range []string{"moved from v0.4.2 to v0.5.0", "`2222222`", "the commits need a minor release"} {
		if !strings.Contains(is.Comments[0], want) {
			t.Errorf("comment lacks %q:\n%s", want, is.Comments[0])
		}
	}

	// The version it would move to already has an issue: say so, rename nothing.
	c2, srv2 := setup(t)
	srv2.Tags = srv.Tags
	srv2.Compares = srv.Compares
	m := srv2.AddIssue(githubtest.Issue{Title: "Release: v0.4.2", Labels: []string{Label}})
	srv2.AddIssue(githubtest.Issue{Title: "Release: v0.5.0", Labels: []string{Label}, State: "closed"})
	r, err = Plan(context.Background(), c2, Options{Ref: "develop"})
	if err != nil || !r.Failed() || issue(srv2, m).Title != "Release: v0.4.2" || len(issue(srv2, m).Comments) != 1 {
		t.Errorf("blocked rename: %v %+v", err, r.Checks)
	}
}

func TestPlanErrors(t *testing.T) {
	ctx := context.Background()
	c, srv := setup(t)
	if _, err := Plan(ctx, c, Options{}); err == nil {
		t.Error("missing ref accepted")
	}
	base := "/repos/blanketops/environments"
	for _, path := range []string{"GET " + base + "/tags", "GET " + base + "/compare", "GET " + base + "/issues",
		"GET " + base + "/git/ref/heads", "PATCH " + base + "/issues", "GET " + base + "/labels", "POST " + base + "/issues"} {
		srv.Issues, srv.Labels = nil, map[string]bool{}
		srv.Tags = []githubtest.Tag{{Name: "v0.4.1"}}
		srv.Compares["v0.4.1...develop"] = []githubtest.Commit{{SHA: "1", Message: "feat!: x"}}
		if strings.HasPrefix(path, "GET "+base+"/git") || strings.HasPrefix(path, "PATCH") {
			srv.AddIssue(githubtest.Issue{Title: "Release: v0.4.2", Labels: []string{Label}})
		}
		srv.FailPaths = map[string]int{path: http.StatusInternalServerError}
		if _, err := Plan(ctx, c, Options{Ref: "develop"}); err == nil {
			t.Errorf("failure of %s not reported", path)
		}
	}
}

func TestCutFirstRelease(t *testing.T) {
	c, srv := setup(t)
	srv.RefCommits["develop"] = []githubtest.Commit{{SHA: "2222222", Message: "feat(releaser): add"}, {SHA: "1111111", Message: "chore: start"}}
	srv.Branches["develop"] = "headsha123456"
	r, err := Cut(context.Background(), c, Options{Ref: "develop", Project: board, RunURL: "https://run/4"})
	if err != nil || r.Failed() {
		t.Fatalf("err %v, checks %+v", err, r.Checks)
	}
	if srv.Branch("release/v0.0.1") != "headsha123456" || r.CutSHA != "headsha123456" {
		t.Fatalf("branches %v", srv.Branches)
	}
	is := srv.Issue("Release: v0.0.1")
	if is == nil || len(is.Comments) != 1 {
		t.Fatalf("issue %+v", is)
	}
	if len(srv.Pulls) != 1 || srv.Pulls[0].Base != "main" || srv.Pulls[0].Head != "release/v0.0.1" || srv.Pulls[0].Title != "release: v0.0.1" ||
		!strings.Contains(srv.Pulls[0].Body, fmt.Sprintf("Tracked in #%d", is.Number)) || !strings.Contains(srv.Pulls[0].Body, "at `headsha`") {
		t.Fatalf("pulls %+v", srv.Pulls)
	}
	for _, want := range []string{"v0.0.1 cut: passed", "`release/v0.0.1` is cut. When it is ready, run Finalize Release with version `v0.0.1`",
		"created `release/v0.0.1` at `headsha` from `develop`", "opened #100"} {
		if !strings.Contains(is.Comments[0], want) {
			t.Errorf("comment lacks %q:\n%s", want, is.Comments[0])
		}
	}

	// A rerun finds the branch and the pull request and cuts nothing.
	if r, err = Cut(context.Background(), c, Options{Ref: "develop", Version: "v0.0.1"}); err != nil || r.Failed() || len(srv.Pulls) != 1 {
		t.Fatalf("rerun: %v %+v, pulls %d", err, r.Checks, len(srv.Pulls))
	}
	if !strings.Contains(checkOf(t, r, "Release branch").Detail, "already exists") || !strings.Contains(checkOf(t, r, "Release pull request").Detail, "#100 is open") {
		t.Errorf("rerun checks %+v", r.Checks)
	}
}

func TestCutPlannedReleaseDryRunAndRefusal(t *testing.T) {
	ctx := context.Background()
	c, srv := setup(t)
	v088, _, _, _ := environments(srv)
	srv.Branches["develop"] = "dev"
	r, err := Cut(ctx, c, Options{Ref: "develop", DryRun: true})
	if err != nil || r.Failed() || r.Version.String() != "v0.8.8" || r.Issue.Number != v088 || len(srv.Branches) != 1 {
		t.Fatalf("dry run: %v %+v", err, r.Checks)
	}
	if !strings.Contains(Comment(r), "Ready to cut `release/v0.8.8`") || !strings.Contains(checkOf(t, r, "Release branch").Detail, "would create") || !strings.Contains(checkOf(t, r, "Release pull request").Detail, "would open") {
		t.Errorf("dry run checks %+v", r.Checks)
	}
	r, err = Cut(ctx, c, Options{Ref: "develop", Version: "v0.8.9"})
	if err != nil || !r.Failed() || len(srv.Branches) != 1 || len(srv.Pulls) != 0 {
		t.Fatalf("skipped version was cut: %v %+v", err, r.Checks)
	}

	// With no planned issue, Cut releases the version the commits need.
	c2, srv2 := setup(t)
	srv2.Tags = []githubtest.Tag{{Name: "v1.2.3"}}
	srv2.Compares["v1.2.3...develop"] = []githubtest.Commit{{SHA: "1", Message: "feat: x"}}
	srv2.Branches["develop"] = "dev"
	if r, err = Cut(ctx, c2, Options{Ref: "develop"}); err != nil || r.Version.String() != "v1.3.0" || srv2.Branch("release/v1.3.0") != "dev" {
		t.Errorf("computed cut: %v %+v", err, r)
	}
}

func TestCutErrors(t *testing.T) {
	ctx := context.Background()
	c, srv := setup(t)
	if _, err := Cut(ctx, c, Options{}); err == nil {
		t.Error("missing ref accepted")
	}
	base := "/repos/blanketops/environments"
	for _, path := range []string{"GET " + base + "/tags", "GET " + base + "/issues", "GET " + base + "/commits",
		"GET " + base + "/git/ref/heads/release", "GET " + base + "/git/ref/heads/develop", "POST " + base + "/git/refs",
		"GET " + base + "/pulls", "POST " + base + "/pulls"} {
		srv.Issues, srv.Labels, srv.Pulls = nil, map[string]bool{}, nil
		srv.Branches = map[string]string{"develop": "dev"}
		srv.RefCommits["develop"] = []githubtest.Commit{{SHA: "1", Message: "feat: x"}}
		srv.FailPaths = map[string]int{path: http.StatusInternalServerError}
		if _, err := Cut(ctx, c, Options{Ref: "develop"}); err == nil {
			t.Errorf("failure of %s not reported", path)
		}
	}
	srv.FailPaths = map[string]int{}
	srv.Issues, srv.Branches = nil, map[string]string{}
	if _, err := Cut(ctx, c, Options{Ref: "develop", Version: "v0.0.1"}); err == nil || !strings.Contains(err.Error(), "branch develop") {
		t.Errorf("missing develop: %v", err)
	}
}

func TestPullBodyWithoutIssue(t *testing.T) {
	r := &Result{Version: issueVersion(github.Issue{Title: "Release: v0.1.0"})}
	if b := PullBody(r); strings.Contains(b, "Tracked in") || !strings.Contains(b, "Release `v0.1.0`, cut from `develop`") {
		t.Errorf("body = %q", b)
	}
	r.Note, r.Stage, r.Warnings = "x", StagePlan, []string{"w"}
	if s := Summary(r); !strings.Contains(s, "- w") {
		t.Errorf("summary = %q", s)
	}
}

func TestPlannedOrderAndCutPrepareError(t *testing.T) {
	prev := issueVersion(github.Issue{Title: "Release: v0.8.7"})
	got := planned(map[string][]github.Issue{
		"v0.9.0": {{Number: 9, State: "open"}, {Number: 4, State: "open"}},
		"v0.8.8": {{Number: 7, State: "closed"}},
		"v0.8.7": {{Number: 1, State: "open"}},
		"":       {{Number: 2, State: "open"}},
	}, &prev)
	if len(got) != 2 || got[0].Number != 4 || got[1].Number != 9 {
		t.Errorf("planned = %+v, want #4 then #9 (same version: oldest first)", got)
	}
	c, srv := setup(t)
	srv.FailPaths = map[string]int{"GET /repos/blanketops/environments/tags": http.StatusInternalServerError}
	if _, err := Cut(context.Background(), c, Options{Ref: "develop", Version: "v0.0.1"}); err == nil {
		t.Error("prepare failure not reported")
	}
}

func TestPrepareKeepsReleaseOrder(t *testing.T) {
	c, srv := setup(t)
	v088, _, _, _ := environments(srv)
	before := len(srv.Issues)
	r, err := Prepare(context.Background(), c, Options{Version: "v0.9.0", Ref: "develop"})
	if err != nil {
		t.Fatal(err)
	}
	if o := checkOf(t, r, "Release order"); o.OK || !strings.Contains(o.Detail, fmt.Sprintf("#%d plans v0.8.8, which comes first", v088)) {
		t.Errorf("order = %+v", o)
	}
	if checkOf(t, r, "Version").OK != true || len(srv.Issues) != before {
		t.Errorf("v0.9.0 is a valid next version but must not get an issue while v0.8.8 is planned: %+v", r.Checks)
	}
}

func TestPlanIgnoresTheSyncMergeAfterARelease(t *testing.T) {
	c, srv := setup(t)
	srv.Tags = []githubtest.Tag{{Name: "v0.0.1"}}
	// finalize-release merges main back into develop with a conventional
	// subject; it is still a merge and carries no change of its own.
	srv.Compares["v0.0.1...develop"] = []githubtest.Commit{{SHA: "s1", Message: "chore: sync develop with main after release/v0.0.1", Parents: 2}}
	r, err := Plan(context.Background(), c, Options{Ref: "develop"})
	if err != nil || len(srv.Issues) != 0 || !strings.Contains(r.Note, "nothing to release") {
		t.Fatalf("plan after a release: %v %+v, issues %d", err, r, len(srv.Issues))
	}
}
