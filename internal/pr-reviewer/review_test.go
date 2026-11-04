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

package prreviewer_test

import (
	"context"
	"strings"
	"testing"

	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
	"github.com/blanketops/environments-bots/internal/pr-reviewer"
)

func api(t *testing.T) *githubtest.Server {
	t.Helper()
	s := githubtest.New("blanketops", "environments", "tok")
	s.Login = "blanketops-environments-pr-reviewer[bot]"
	t.Cleanup(s.Close)
	return s
}

func client(t *testing.T, s *githubtest.Server) *github.Client {
	t.Helper()
	c, err := github.New(s.URL, "tok", "blanketops/environments")
	if err != nil {
		t.Fatalf("github.New: %v", err)
	}
	return c
}

func TestReviewPostsCommentAndLabels(t *testing.T) {
	s := api(t)
	s.Pulls = append(s.Pulls, githubtest.Pull{
		Number: 7,
		Title:  "feat: add widgets",
		State:  "open",
		Base:   "develop",
		Head:   "feat/widgets",
	})
	s.Compares["develop...feat/widgets"] = []githubtest.Commit{
		{SHA: "c0ffee1", Message: "feat: add widgets"},
	}
	// A Go source file with no matching test file.
	s.PullFiles[7] = []github.PullFile{
		{Filename: "resolution/environment/resolve/resolve.go", Status: "modified", Additions: 20, Deletions: 5,
			Patch: "@@ -1,3 +1,4 @@\n func ResolveEnvironment(...) {\n+	return nil\n }\n"},
	}
	// PR template with checklist.
	s.PullFiles[0] = nil // not used; contents served by GetContents stub

	api := client(t, s)
	res, err := prreviewer.Review(context.Background(), api, prreviewer.ReviewOptions{
		Repo:   "blanketops/environments",
		PR:     7,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if res.PR != 7 {
		t.Fatalf("PR = %d, want 7", res.PR)
	}
	if res.Title != "feat: add widgets" {
		t.Fatalf("title = %q, want feat: add widgets", res.Title)
	}
	// Should have labeled feat + domain.
	hasFeat := false
	hasDomain := false
	for _, l := range res.Labels {
		if l == "feat" {
			hasFeat = true
		}
		if l == "domain" {
			hasDomain = true
		}
	}
	if !hasFeat {
		t.Errorf("labels = %v, want feat", res.Labels)
	}
	if !hasDomain {
		t.Errorf("labels = %v, want domain", res.Labels)
	}
	// Comment should mention the missing test file.
	if !strings.Contains(res.Comment, "resolve.go") {
		t.Errorf("comment does not mention changed file: %s", res.Comment)
	}
	if !strings.Contains(res.Comment, "resolve_test.go") {
		t.Errorf("comment does not suggest test file: %s", res.Comment)
	}
	// Dry run: nothing posted.
	if len(s.Pulls[0].Comments) != 0 {
		t.Errorf("dry run posted %d comments, want 0", len(s.Pulls[0].Comments))
	}
}

func TestReviewSecretDetection(t *testing.T) {
	s := api(t)
	s.Pulls = append(s.Pulls, githubtest.Pull{
		Number: 3,
		Title:  "fix: resolve secrets bug",
		State:  "open",
		Base:   "develop",
		Head:   "fix/secrets",
	})
	s.PullFiles[3] = []github.PullFile{
		{Filename: "pkg/secrets/git/build/build.go", Status: "modified", Additions: 5, Deletions: 1,
			Patch: "@@ -1,3 +1,4 @@\n apiKey = \"sk-1234567890abcdef1234567890abcdef\"\n"},
	}

	api := client(t, s)
	res, err := prreviewer.Review(context.Background(), api, prreviewer.ReviewOptions{
		Repo:   "blanketops/environments",
		PR:     3,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	foundSecret := false
	for _, iss := range res.Issues {
		if iss.Severity == "error" && strings.Contains(iss.Message, "secret") {
			foundSecret = true
		}
	}
	if !foundSecret {
		t.Errorf("issues = %v, want a secret error", res.Issues)
	}
}

func TestReviewChecklist(t *testing.T) {
	s := api(t)
	s.Pulls = append(s.Pulls, githubtest.Pull{
		Number: 5,
		Title:  "docs: update readme",
		State:  "open",
		Base:   "develop",
		Head:   "docs/readme",
	})
	s.PullFiles[5] = []github.PullFile{
		{Filename: "README.md", Status: "modified", Additions: 3, Deletions: 1},
	}
	// PR body (in title) mentions checklist item.
	s.Pulls[0].Title = "docs: update readme with checklist item"

	api := client(t, s)
	res, err := prreviewer.Review(context.Background(), api, prreviewer.ReviewOptions{
		Repo:   "blanketops/environments",
		PR:     5,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	// Should have docs label.
	hasDocs := false
	for _, l := range res.Labels {
		if l == "docs" {
			hasDocs = true
		}
	}
	if !hasDocs {
		t.Errorf("labels = %v, want docs", res.Labels)
	}
}
