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

func tidyServer(t *testing.T, branch string, withPull bool) (*githubtest.Server, develop.TidyAPI) {
	t.Helper()
	s := githubtest.New("blanketops", "environments-bots", token)
	s.Login = "blanketops-environments-developer[bot]"
	s.Branches["develop"] = "devhead00"
	s.Branches[branch] = "branchhd0"
	if withPull {
		s.Pulls = append(s.Pulls, githubtest.Pull{Number: 20, Title: "feat: x", State: "open", Base: "develop", Head: branch})
	}
	t.Cleanup(s.Close)
	api, err := github.New(s.URL, token, "blanketops/environments-bots")
	if err != nil {
		t.Fatalf("github.New: %v", err)
	}
	return s, api
}

func TestTidyCommitsAndComments(t *testing.T) {
	s, api := tidyServer(t, "feat/x", true)
	changes := []develop.TidyChange{
		{Path: "main.go", Content: []byte("package main\n")},
		{Path: "go.sum", Deleted: true},
	}
	res, err := develop.Tidy(context.Background(), api, develop.TidyOptions{Branch: "feat/x", Changes: changes})
	if err != nil {
		t.Fatalf("Tidy: %v", err)
	}
	if res.Clean || res.CommitOID == "" {
		t.Fatalf("want a commit, got %+v", res)
	}
	if len(s.CommitsOnBranch) != 1 {
		t.Fatalf("want one signed commit, got %d", len(s.CommitsOnBranch))
	}
	cob := s.CommitsOnBranch[0]
	if cob.Branch != "feat/x" || cob.Headline != develop.TidyMessage {
		t.Fatalf("commit = %+v", cob)
	}
	if len(cob.Additions) != 1 || cob.Additions[0] != "main.go" || len(cob.Deletions) != 1 || cob.Deletions[0] != "go.sum" {
		t.Fatalf("commit file changes = %+v", cob)
	}
	// The open pull request is commented.
	if res.Pull == nil || res.Pull.Number != 20 {
		t.Fatalf("want PR #20 commented, got %+v", res.Pull)
	}
	is := s.PullComments(20)
	if len(is) == 0 || !strings.Contains(is[0], "Tidied 2 file(s)") {
		t.Fatalf("pull not commented: %v", is)
	}
}

func TestTidyClean(t *testing.T) {
	s, api := tidyServer(t, "feat/x", true)
	res, err := develop.Tidy(context.Background(), api, develop.TidyOptions{Branch: "feat/x"})
	if err != nil {
		t.Fatalf("Tidy: %v", err)
	}
	if !res.Clean {
		t.Fatalf("want Clean, got %+v", res)
	}
	if len(s.CommitsOnBranch) != 0 {
		t.Fatal("clean tidy made a commit")
	}
	if sum := develop.TidySummary(res, false); !strings.Contains(sum, "Already tidy") {
		t.Fatalf("summary = %q", sum)
	}
}

func TestTidyDryRun(t *testing.T) {
	s, api := tidyServer(t, "feat/x", true)
	changes := []develop.TidyChange{{Path: "main.go", Content: []byte("package main\n")}}
	res, err := develop.Tidy(context.Background(), api, develop.TidyOptions{Branch: "feat/x", Changes: changes, DryRun: true})
	if err != nil {
		t.Fatalf("Tidy: %v", err)
	}
	if res.CommitOID != "" || len(s.CommitsOnBranch) != 0 {
		t.Fatalf("dry run committed: %+v", res)
	}
	if len(res.Added) != 1 {
		t.Fatalf("want 1 would-add, got %d", len(res.Added))
	}
	if sum := develop.TidySummary(res, true); !strings.Contains(sum, "Would commit") {
		t.Fatalf("summary = %q", sum)
	}
}

func TestTidyCommitsWithoutOpenPull(t *testing.T) {
	s, api := tidyServer(t, "feat/x", false)
	changes := []develop.TidyChange{{Path: "main.go", Content: []byte("package main\n")}}
	res, err := develop.Tidy(context.Background(), api, develop.TidyOptions{Branch: "feat/x", Changes: changes})
	if err != nil {
		t.Fatalf("Tidy: %v", err)
	}
	if res.CommitOID == "" || res.Pull != nil {
		t.Fatalf("want a commit and no PR comment, got %+v", res)
	}
	if len(s.CommitsOnBranch) != 1 {
		t.Fatalf("want one commit, got %d", len(s.CommitsOnBranch))
	}
}

func TestTidyNoBranch(t *testing.T) {
	_, api := tidyServer(t, "feat/x", true)
	changes := []develop.TidyChange{{Path: "main.go", Content: []byte("package main\n")}}
	if _, err := develop.Tidy(context.Background(), api, develop.TidyOptions{Changes: changes}); err == nil {
		t.Fatal("want error when no branch is given")
	}
}
