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
	"net/http"
	"strings"
	"testing"

	"github.com/blanketops/environments-bots/internal/develop"
	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
)

const token = "t0ken"

// newServer starts a fake with main and develop branches set, and behind as
// the commits main has that develop lacks (empty: develop is level).
func newServer(t *testing.T, behind []githubtest.Commit) (*githubtest.Server, develop.API) {
	t.Helper()
	s := githubtest.New("blanketops", "environments-bots", token)
	s.Login = "blanketops-environments-developer[bot]"
	s.Branches["main"] = "mainhead0"
	s.Branches["develop"] = "devhead00"
	s.Compares["develop...main"] = behind
	t.Cleanup(s.Close)
	api, err := github.New(s.URL, token, "blanketops/environments-bots")
	if err != nil {
		t.Fatalf("github.New: %v", err)
	}
	return s, api
}

func commits(n int) []githubtest.Commit {
	cs := make([]githubtest.Commit, n)
	for i := range cs {
		cs[i] = githubtest.Commit{SHA: strings.Repeat("a", 6) + string(rune('1'+i)), Message: "feat: thing"}
	}
	return cs
}

func TestSyncUpToDate(t *testing.T) {
	s, api := newServer(t, nil)
	res, err := develop.Sync(context.Background(), api, develop.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !res.UpToDate {
		t.Fatalf("want UpToDate, got %+v", res)
	}
	if len(s.Pulls) != 0 || len(s.Merges) != 0 {
		t.Fatalf("up-to-date sync wrote: pulls=%d merges=%d", len(s.Pulls), len(s.Merges))
	}
	if sum := develop.SyncSummary(res, false); !strings.Contains(sum, "up to date") {
		t.Fatalf("summary = %q", sum)
	}
}

func TestSyncOpensPull(t *testing.T) {
	s, api := newServer(t, commits(3))
	res, err := develop.Sync(context.Background(), api, develop.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Opened == nil {
		t.Fatalf("want a pull request opened, got %+v", res)
	}
	if len(s.Merges) != 1 {
		t.Fatalf("want one merge, got %d", len(s.Merges))
	}
	m := s.Merges[0]
	if m.Head != "main" || !strings.HasPrefix(m.Base, "sync/main-into-develop-") {
		t.Fatalf("merge = %+v", m)
	}
	if len(s.Pulls) != 1 {
		t.Fatalf("want one pull request, got %d", len(s.Pulls))
	}
	p := s.Pulls[0]
	if p.Base != "develop" || !strings.HasPrefix(p.Head, "sync/main-into-develop-") {
		t.Fatalf("pull = %+v", p)
	}
	if p.Title != "chore: sync develop with main" {
		t.Fatalf("title = %q", p.Title)
	}
	if !strings.Contains(p.Body, "3 commit(s) behind") {
		t.Fatalf("body = %q", p.Body)
	}
	// The branch was created off develop before the merge.
	if s.Branch(p.Head) == "" {
		t.Fatalf("branch %q not created", p.Head)
	}
}

func TestSyncExistingPull(t *testing.T) {
	s, api := newServer(t, commits(2))
	s.Pulls = append(s.Pulls, githubtest.Pull{
		Number: 42, Title: "chore: sync develop with main", State: "open",
		Base: "develop", Head: "sync/main-into-develop-older00",
	})
	res, err := develop.Sync(context.Background(), api, develop.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Existing == nil || res.Existing.Number != 42 {
		t.Fatalf("want existing #42, got %+v", res)
	}
	if res.Opened != nil || len(s.Merges) != 0 {
		t.Fatalf("existing sync should open nothing: %+v merges=%d", res.Opened, len(s.Merges))
	}
}

func TestSyncConflict(t *testing.T) {
	s, api := newServer(t, commits(1))
	s.FailPaths["POST /repos/blanketops/environments-bots/merges"] = http.StatusConflict
	res, err := develop.Sync(context.Background(), api, develop.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !res.Conflict {
		t.Fatalf("want Conflict, got %+v", res)
	}
	if res.Opened != nil {
		t.Fatalf("conflict should open no pull request")
	}
	// The branch created before the failed merge is cleaned up.
	if got := branchesWithPrefix(s, "sync/main-into-develop-"); got != 0 {
		t.Fatalf("conflict left %d sync branch(es)", got)
	}
	if sum := develop.SyncSummary(res, false); !strings.Contains(sum, "by hand") {
		t.Fatalf("summary = %q", sum)
	}
}

func TestSyncDryRun(t *testing.T) {
	s, api := newServer(t, commits(2))
	res, err := develop.Sync(context.Background(), api, develop.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.UpToDate || res.Opened != nil {
		t.Fatalf("dry run should report only, got %+v", res)
	}
	if len(res.Behind) != 2 {
		t.Fatalf("want 2 behind, got %d", len(res.Behind))
	}
	if len(s.Pulls) != 0 || len(s.Merges) != 0 || branchesWithPrefix(s, "sync/") != 0 {
		t.Fatalf("dry run wrote to the repo")
	}
	if sum := develop.SyncSummary(res, true); !strings.Contains(sum, "Would open") {
		t.Fatalf("summary = %q", sum)
	}
}

func TestSyncReusesLeftoverBranch(t *testing.T) {
	s, api := newServer(t, commits(1))
	// A previous run left the branch behind with no pull request.
	branch := "sync/main-into-develop-" + "mainhea"
	s.Branches[branch] = "devhead00"
	res, err := develop.Sync(context.Background(), api, develop.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Opened == nil {
		t.Fatalf("want a pull request opened on the reused branch, got %+v", res)
	}
	if res.Branch != branch {
		t.Fatalf("branch = %q, want %q", res.Branch, branch)
	}
}

func TestSyncCustomBranches(t *testing.T) {
	s := githubtest.New("blanketops", "environments-bots", token)
	s.Branches["master"] = "masterhd0"
	s.Branches["release"] = "relhead00"
	s.Compares["release...master"] = commits(1)
	t.Cleanup(s.Close)
	api, err := github.New(s.URL, token, "blanketops/environments-bots")
	if err != nil {
		t.Fatalf("github.New: %v", err)
	}
	res, err := develop.Sync(context.Background(), api, develop.SyncOptions{Base: "release", From: "master"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Opened == nil {
		t.Fatalf("want a pull request opened, got %+v", res)
	}
	if len(s.Pulls) != 1 || s.Pulls[0].Base != "release" || s.Pulls[0].Head != "sync/main-into-develop-masterh" {
		t.Fatalf("want pull request into release from the sync branch, got %+v", s.Pulls)
	}
}

func TestSyncBranchError(t *testing.T) {
	// main missing: BranchSHA fails.
	s := githubtest.New("blanketops", "environments-bots", token)
	s.Branches["develop"] = "devhead00"
	t.Cleanup(s.Close)
	api, err := github.New(s.URL, token, "blanketops/environments-bots")
	if err != nil {
		t.Fatalf("github.New: %v", err)
	}
	if _, err := develop.Sync(context.Background(), api, develop.SyncOptions{}); err == nil {
		t.Fatal("want error when main is missing")
	}
}

func branchesWithPrefix(s *githubtest.Server, prefix string) int {
	n := 0
	// Branch is the only concurrency-safe reader; probe known names via it is
	// awkward, so read the map through a harmless Branch call per candidate is
	// not possible. Use the exported map snapshot instead.
	for name := range snapshotBranches(s) {
		if strings.HasPrefix(name, prefix) {
			n++
		}
	}
	return n
}

// snapshotBranches copies the fake's branch names. The fake guards Branches
// with a mutex only inside its handlers; tests run after Sync returns, with
// no request in flight, so a direct read is safe here.
func snapshotBranches(s *githubtest.Server) map[string]string {
	out := map[string]string{}
	for k, v := range s.Branches {
		out[k] = v
	}
	return out
}
