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

// resolveServer starts a fake with a pull request (merged or not) from branch
// and, unless skipIssue, its open tracking issue.
func resolveServer(t *testing.T, branch string, merged, skipIssue bool) (*githubtest.Server, develop.ResolveAPI) {
	t.Helper()
	s := githubtest.New("blanketops", "environments-bots", token)
	s.Login = "blanketops-environments-developer[bot]"
	s.Pulls = append(s.Pulls, githubtest.Pull{Number: 7, Title: "feat: thing", State: "closed", Base: "develop", Head: branch, Merged: merged})
	if !skipIssue {
		s.AddIssue(githubtest.Issue{Title: "[pr] " + branch, State: "open", Labels: []string{develop.TrackLabel, "feat"}})
	}
	t.Cleanup(s.Close)
	api, err := github.New(s.URL, token, "blanketops/environments-bots")
	if err != nil {
		t.Fatalf("github.New: %v", err)
	}
	return s, api
}

func TestResolveClosesIssueOnMerge(t *testing.T) {
	s, api := resolveServer(t, "feat/widgets", true, false)
	res, err := develop.Resolve(context.Background(), api, develop.ResolveOptions{Pull: 7})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.Closed {
		t.Fatalf("want Closed, got %+v", res)
	}
	is := s.Issue("[pr] feat/widgets")
	if is == nil || is.State != "closed" {
		t.Fatalf("tracking issue not closed: %+v", is)
	}
	linked := false
	for _, c := range is.Comments {
		if strings.Contains(c, "Closed by merged pull request #7") {
			linked = true
		}
	}
	if !linked {
		t.Fatalf("merge not commented on the issue: %v", is.Comments)
	}
}

func TestResolveSkipsWhenNotMerged(t *testing.T) {
	s, api := resolveServer(t, "feat/widgets", false, false)
	res, err := develop.Resolve(context.Background(), api, develop.ResolveOptions{Pull: 7})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.NotMerged {
		t.Fatalf("want NotMerged, got %+v", res)
	}
	if is := s.Issue("[pr] feat/widgets"); is == nil || is.State != "open" {
		t.Fatalf("tracking issue should stay open: %+v", is)
	}
}

func TestResolveNoTrackingIssue(t *testing.T) {
	_, api := resolveServer(t, "feat/widgets", true, true)
	res, err := develop.Resolve(context.Background(), api, develop.ResolveOptions{Pull: 7})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.NotFound {
		t.Fatalf("want NotFound, got %+v", res)
	}
}

func TestResolveAlreadyClosed(t *testing.T) {
	s, api := resolveServer(t, "feat/widgets", true, true)
	s.AddIssue(githubtest.Issue{Title: "[pr] feat/widgets", State: "closed", Labels: []string{develop.TrackLabel}})
	res, err := develop.Resolve(context.Background(), api, develop.ResolveOptions{Pull: 7})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.AlreadyClosed {
		t.Fatalf("want AlreadyClosed, got %+v", res)
	}
}

func TestResolveDryRun(t *testing.T) {
	s, api := resolveServer(t, "feat/widgets", true, false)
	res, err := develop.Resolve(context.Background(), api, develop.ResolveOptions{Pull: 7, DryRun: true})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Closed {
		t.Fatalf("dry run should not close")
	}
	if is := s.Issue("[pr] feat/widgets"); is.State != "open" || len(is.Comments) != 0 {
		t.Fatalf("dry run wrote to the issue: %+v", is)
	}
	if sum := develop.ResolveSummary(res, true); !strings.Contains(sum, "Would close") {
		t.Fatalf("summary = %q", sum)
	}
}

func TestResolveNoPull(t *testing.T) {
	_, api := resolveServer(t, "feat/widgets", true, false)
	if _, err := develop.Resolve(context.Background(), api, develop.ResolveOptions{}); err == nil {
		t.Fatal("want error when no pull request number is given")
	}
}
