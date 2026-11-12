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

package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
)

const (
	testOwner = "blanketops"
	testRepo  = "environments-install"
	testToken = "tok"
)

func newAPI(t *testing.T, s *githubtest.Server) *github.Client {
	t.Helper()
	c, err := github.New(s.URL, testToken, s.Owner+"/"+s.Repo)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestSync_UpToDate(t *testing.T) {
	s := githubtest.New(testOwner, testRepo, testToken)
	defer s.Close()
	s.Files["config/crd/bases/a.yaml"] = "A"

	res, err := Sync(context.Background(), newAPI(t, s), SyncOptions{
		DestPath: "config/crd/bases", Base: "main",
		Files: []File{{Name: "a.yaml", Content: "A"}}, AutoMerge: true,
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !res.UpToDate {
		t.Fatalf("want UpToDate, got %+v", res)
	}
	if res.Pull != nil || len(s.Pulls) != 0 {
		t.Fatalf("no pull request expected, got %+v / %d", res.Pull, len(s.Pulls))
	}
	if len(s.CommitsOnBranch) != 0 {
		t.Fatalf("no commit expected, got %d", len(s.CommitsOnBranch))
	}
}

func TestSync_OpensAndMerges(t *testing.T) {
	s := githubtest.New(testOwner, testRepo, testToken)
	defer s.Close()
	s.Branches["main"] = "basesha0123456789"
	s.Files["config/crd/bases/a.yaml"] = "OLD" // differs → changed
	// b.yaml is absent → new → changed

	res, err := Sync(context.Background(), newAPI(t, s), SyncOptions{
		DestPath: "config/crd/bases", Base: "main",
		Files: []File{
			{Name: "a.yaml", Content: "NEW"},
			{Name: "b.yaml", Content: "B"},
		},
		Label: "CRDs from environments-api v0.3.2", AutoMerge: true,
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.UpToDate {
		t.Fatal("did not expect UpToDate")
	}
	wantChanged := []string{"config/crd/bases/a.yaml", "config/crd/bases/b.yaml"}
	if strings.Join(res.Changed, ",") != strings.Join(wantChanged, ",") {
		t.Fatalf("Changed = %v, want %v", res.Changed, wantChanged)
	}
	if !strings.HasPrefix(res.Branch, "sync/manifests-config-crd-bases-") {
		t.Fatalf("branch %q lacks sync/ manifests prefix", res.Branch)
	}
	if res.Pull == nil || len(s.Pulls) != 1 {
		t.Fatalf("expected one pull request, got %+v / %d", res.Pull, len(s.Pulls))
	}
	if !res.Merged || !s.Pulls[0].Merged {
		t.Fatalf("expected the pull request merged, got res.Merged=%v pull.Merged=%v", res.Merged, s.Pulls[0].Merged)
	}
	if len(s.CommitsOnBranch) != 1 {
		t.Fatalf("expected one signed commit, got %d", len(s.CommitsOnBranch))
	}
	if got := len(s.CommitsOnBranch[0].Additions); got != 2 {
		t.Fatalf("commit wrote %d files, want 2", got)
	}
	if _, left := s.Branches[res.Branch]; left {
		t.Fatalf("merged branch %q should be deleted", res.Branch)
	}
}

func TestSync_DryRunWritesNothing(t *testing.T) {
	s := githubtest.New(testOwner, testRepo, testToken)
	defer s.Close()
	s.Branches["main"] = "basesha0123456789"
	// no existing file → would change

	res, err := Sync(context.Background(), newAPI(t, s), SyncOptions{
		DestPath: "config/rbac", Base: "main",
		Files: []File{{Name: "role.yaml", Content: "rules"}}, DryRun: true, AutoMerge: true,
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Fatalf("Changed = %v, want one entry", res.Changed)
	}
	if res.Pull != nil || len(s.Pulls) != 0 || len(s.CommitsOnBranch) != 0 {
		t.Fatal("dry run must write nothing")
	}
}

func TestSync_NoAutoMergeLeavesPullOpen(t *testing.T) {
	s := githubtest.New(testOwner, testRepo, testToken)
	defer s.Close()
	s.Branches["main"] = "basesha0123456789"

	res, err := Sync(context.Background(), newAPI(t, s), SyncOptions{
		DestPath: "config/crd/bases", Base: "main",
		Files: []File{{Name: "a.yaml", Content: "A"}}, AutoMerge: false,
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Pull == nil {
		t.Fatal("expected a pull request")
	}
	if res.Merged || s.Pulls[0].Merged {
		t.Fatal("must not merge when AutoMerge is false")
	}
	if _, ok := s.Branches[res.Branch]; !ok {
		t.Fatalf("branch %q should remain for the open pull request", res.Branch)
	}
}

func TestSync_RecreatesStaleBranch(t *testing.T) {
	s := githubtest.New(testOwner, testRepo, testToken)
	defer s.Close()
	s.Branches["main"] = "basesha0123456789"
	// A branch a previous interrupted run left behind, at the deterministic
	// sync name. CreateBranch must see "already exists" (422), delete it, and
	// recreate at the base — not fail.
	s.Branches["sync/manifests-config-crd-bases-basesha"] = "staleoid"

	res, err := Sync(context.Background(), newAPI(t, s), SyncOptions{
		DestPath: "config/crd/bases", Base: "main",
		Files: []File{{Name: "a.yaml", Content: "A"}}, AutoMerge: false,
	})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Branch != "sync/manifests-config-crd-bases-basesha" {
		t.Fatalf("branch = %q", res.Branch)
	}
	if res.Pull == nil || len(s.Pulls) != 1 {
		t.Fatalf("expected the stale branch recreated and a PR opened, got %+v", res)
	}
	if len(s.CommitsOnBranch) != 1 {
		t.Fatalf("expected one signed commit, got %d", len(s.CommitsOnBranch))
	}
}

func TestSync_MergeFailureSurfaces(t *testing.T) {
	s := githubtest.New(testOwner, testRepo, testToken)
	defer s.Close()
	s.Branches["main"] = "basesha0123456789"
	// Fail the merge: the pull request opens, then the merge call 405s.
	s.FailPaths["PUT /repos/blanketops/environments-install/pulls/100/merge"] = http.StatusMethodNotAllowed

	_, err := Sync(context.Background(), newAPI(t, s), SyncOptions{
		DestPath: "config/crd/bases", Base: "main",
		Files: []File{{Name: "a.yaml", Content: "A"}}, AutoMerge: true,
	})
	if err == nil || !strings.Contains(err.Error(), "merge pull request") {
		t.Fatalf("want a merge error, got %v", err)
	}
}

func TestSync_Validation(t *testing.T) {
	s := githubtest.New(testOwner, testRepo, testToken)
	defer s.Close()
	api := newAPI(t, s)
	cases := []SyncOptions{
		{Base: "main", Files: []File{{Name: "a.yaml", Content: "A"}}},   // no dest path
		{DestPath: "config/crd/bases", Files: []File{{Name: "a.yaml"}}}, // no base
		{DestPath: "config/crd/bases", Base: "main"},                    // no files
	}
	for i, opts := range cases {
		if _, err := Sync(context.Background(), api, opts); err == nil {
			t.Fatalf("case %d: expected a validation error", i)
		}
	}
}
