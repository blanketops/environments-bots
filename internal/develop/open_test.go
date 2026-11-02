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

package develop_test

import (
	"context"
	"strings"
	"testing"

	"github.com/blanketops/environments-bots/internal/develop"
	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
)

// openServer starts a fake with a feature branch that has one commit and no
// pull request.
func openServer(t *testing.T, branch, commitSubject string) (*githubtest.Server, develop.OpenAPI) {
	t.Helper()
	s := githubtest.New("blanketops", "environments-bots", token)
	s.Login = "blanketops-environments-developer[bot]"
	s.Branches["develop"] = "devhead00"
	s.Branches[branch] = "branchhd0"
	s.RefCommits[branch] = []githubtest.Commit{{SHA: "c0ffee1", Message: commitSubject}}
	t.Cleanup(s.Close)
	api, err := github.New(s.URL, token, "blanketops/environments-bots")
	if err != nil {
		t.Fatalf("github.New: %v", err)
	}
	return s, api
}

func TestOpenCreatesPullIssueAndLabels(t *testing.T) {
	s, api := openServer(t, "feat/widgets", "feat: add widgets")
	res, err := develop.Open(context.Background(), api, develop.OpenOptions{Branch: "feat/widgets"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.Pull == nil || res.Issue == nil {
		t.Fatalf("want a pull request and an issue, got %+v", res)
	}
	if len(s.Pulls) != 1 {
		t.Fatalf("want one pull request, got %d", len(s.Pulls))
	}
	p := s.Pulls[0]
	if p.Base != "develop" || p.Head != "feat/widgets" {
		t.Fatalf("pull = %+v", p)
	}
	if p.Title != "feat: add widgets" {
		t.Fatalf("title = %q, want the commit subject", p.Title)
	}
	// The pull request links the tracking issue.
	if !strings.Contains(p.Body, "Tracks #1") {
		t.Fatalf("pull body does not link the issue: %q", p.Body)
	}
	// The pull request is labelled pr-tracking + the branch's type.
	if !hasLabel(p.Labels, develop.TrackLabel) || !hasLabel(p.Labels, "feat") {
		t.Fatalf("pull labels = %v, want %s + feat", p.Labels, develop.TrackLabel)
	}
	// The tracking issue is titled per branch, labelled, and links the PR.
	is := s.Issue("[pr] feat/widgets")
	if is == nil {
		t.Fatal("tracking issue [pr] feat/widgets not opened")
	}
	if !hasLabel(is.Labels, develop.TrackLabel) || !hasLabel(is.Labels, "feat") {
		t.Fatalf("issue labels = %v", is.Labels)
	}
	linked := false
	for _, c := range is.Comments {
		if strings.Contains(c, "Pull request: #") {
			linked = true
		}
	}
	if !linked {
		t.Fatalf("issue not linked to the pull request: %v", is.Comments)
	}
}

func TestOpenIdempotentWhenPullExists(t *testing.T) {
	s, api := openServer(t, "feat/widgets", "feat: add widgets")
	s.Pulls = append(s.Pulls, githubtest.Pull{Number: 50, Title: "feat: add widgets", State: "open", Base: "develop", Head: "feat/widgets"})
	res, err := develop.Open(context.Background(), api, develop.OpenOptions{Branch: "feat/widgets"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.Existing == nil || res.Existing.Number != 50 {
		t.Fatalf("want existing #50, got %+v", res)
	}
	if res.Pull != nil || res.Issue != nil || len(s.Issues) != 0 {
		t.Fatalf("a second push should open nothing: %+v, issues=%d", res, len(s.Issues))
	}
}

func TestOpenSkipsLongLivedBranches(t *testing.T) {
	for _, branch := range []string{"main", "develop", "release/v0.0.4", "sync/main-into-develop-abc1234"} {
		s, api := openServer(t, branch, "whatever")
		res, err := develop.Open(context.Background(), api, develop.OpenOptions{Branch: branch})
		if err != nil {
			t.Fatalf("Open(%s): %v", branch, err)
		}
		if res.Skipped == "" {
			t.Errorf("branch %s: want skipped, got %+v", branch, res)
		}
		if len(s.Pulls) != 0 || len(s.Issues) != 0 {
			t.Errorf("branch %s: opened something", branch)
		}
	}
}

func TestOpenDryRun(t *testing.T) {
	s, api := openServer(t, "fix/thing", "fix: the thing")
	res, err := develop.Open(context.Background(), api, develop.OpenOptions{Branch: "fix/thing", DryRun: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.Pull != nil || res.Issue != nil {
		t.Fatalf("dry run should create nothing: %+v", res)
	}
	if res.Title != "fix: the thing" {
		t.Fatalf("title = %q", res.Title)
	}
	if !hasLabel(res.Labels, "fix") {
		t.Fatalf("labels = %v, want fix", res.Labels)
	}
	if len(s.Pulls) != 0 || len(s.Issues) != 0 || len(s.Labels) != 0 {
		t.Fatal("dry run wrote to the repo")
	}
	if sum := develop.OpenSummary(res, true); !strings.Contains(sum, "Would open") {
		t.Fatalf("summary = %q", sum)
	}
}

func TestOpenReusesOpenTrackingIssue(t *testing.T) {
	s, api := openServer(t, "feat/widgets", "feat: add widgets")
	// A previous pull request was closed, leaving its tracking issue open.
	s.AddIssue(githubtest.Issue{Title: "[pr] feat/widgets", State: "open", Labels: []string{develop.TrackLabel, "feat"}})
	res, err := develop.Open(context.Background(), api, develop.OpenOptions{Branch: "feat/widgets"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.Issue == nil || res.Issue.Number != 1 {
		t.Fatalf("want the existing issue #1 reused, got %+v", res.Issue)
	}
	if len(s.Issues) != 1 {
		t.Fatalf("want no duplicate issue, got %d", len(s.Issues))
	}
	if res.Pull == nil {
		t.Fatal("want a pull request opened on the reused issue")
	}
}

func TestOpenNoTypeLabelForPlainBranch(t *testing.T) {
	s, api := openServer(t, "experiment", "try something")
	res, err := develop.Open(context.Background(), api, develop.OpenOptions{Branch: "experiment"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(res.Labels) != 1 || res.Labels[0] != develop.TrackLabel {
		t.Fatalf("labels = %v, want only %s", res.Labels, develop.TrackLabel)
	}
	_ = s
}

func TestOpenNoBranch(t *testing.T) {
	_, api := openServer(t, "feat/x", "feat: x")
	if _, err := develop.Open(context.Background(), api, develop.OpenOptions{}); err == nil {
		t.Fatal("want error when no branch is given")
	}
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}
