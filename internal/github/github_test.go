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

package github_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
)

func newClient(t *testing.T) (*github.Client, *githubtest.Server) {
	t.Helper()
	srv := githubtest.New("blanketops", "environments-tests", "tok")
	t.Cleanup(srv.Close)
	c, err := github.New(srv.URL+"/", "tok", "blanketops/environments-tests")
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestNew(t *testing.T) {
	for _, bad := range []string{"", "owner", "/repo", "owner/", "a/b/c"} {
		if _, err := github.New("", "", bad); err == nil {
			t.Errorf("New(%q) accepted a bad repository", bad)
		}
	}
	c, err := github.New("", "", "o/r")
	if err != nil || c.BaseURL != github.DefaultBaseURL || c.Owner != "o" || c.Repo != "r" {
		t.Errorf("New(o/r) = %+v, %v", c, err)
	}
}

func TestIssues(t *testing.T) {
	c, srv := newClient(t)
	ctx := context.Background()
	srv.PerPage = 2 // force pagination
	srv.AddIssue(githubtest.Issue{Title: "[test] a", Labels: []string{"test-tracking"}})
	srv.AddIssue(githubtest.Issue{Title: "[test] b", Labels: []string{"test-tracking"}, State: "closed"})
	srv.AddIssue(githubtest.Issue{Title: "a PR", Labels: []string{"test-tracking"}, PR: true})
	srv.AddIssue(githubtest.Issue{Title: "unrelated", Labels: []string{"bug"}})
	srv.AddIssue(githubtest.Issue{Title: "[test] c", Labels: []string{"test-tracking"}})

	issues, err := c.ListIssues(ctx, "test-tracking")
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, is := range issues {
		titles = append(titles, is.Title+"/"+is.State)
	}
	if got, want := strings.Join(titles, ","), "[test] a/open,[test] b/closed,[test] c/open"; got != want {
		t.Errorf("ListIssues = %s, want %s (all states, no PRs, every page)", got, want)
	}

	is, err := c.CreateIssue(ctx, "[test] d", "body", []string{"test-tracking", "grpc"})
	if err != nil {
		t.Fatal(err)
	}
	if is.Number != 6 || is.NodeID != "I_6" || !strings.HasSuffix(is.HTMLURL, "/issues/6") {
		t.Errorf("CreateIssue = %+v", is)
	}
	if err := c.Comment(ctx, 6, "hello"); err != nil {
		t.Fatal(err)
	}
	if got := srv.Issue("[test] d").Comments; len(got) != 1 || got[0] != "hello" {
		t.Errorf("comments = %v", got)
	}
	err = c.Comment(ctx, 99, "nobody")
	if !github.IsNotFound(err) {
		t.Errorf("Comment on a missing issue: err = %v, want 404", err)
	}
}

func TestEnsureLabel(t *testing.T) {
	c, srv := newClient(t)
	ctx := context.Background()
	if err := c.EnsureLabel(ctx, "test-tracking", "0075ca", "d"); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureLabel(ctx, "test-tracking", "0075ca", "d"); err != nil {
		t.Fatal(err)
	}
	posts := 0
	for _, r := range srv.Requests {
		if r == "POST /repos/blanketops/environments-tests/labels" {
			posts++
		}
	}
	if posts != 1 {
		t.Errorf("label created %d times, want once: %v", posts, srv.Requests)
	}
	srv.FailPaths["GET /repos/blanketops/environments-tests/labels/"] = http.StatusForbidden
	if err := c.EnsureLabel(ctx, "other", "000000", ""); err == nil || github.IsNotFound(err) {
		t.Errorf("EnsureLabel with a 403: err = %v, want it returned", err)
	}
}

func TestProject(t *testing.T) {
	c, srv := newClient(t)
	ctx := context.Background()

	p, err := c.FindProject(ctx, "blanketops", 1)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "PVT_1" || p.StatusFieldID != "FIELD_status" || p.StatusOptions["Test"] != "opt-test" {
		t.Errorf("FindProject = %+v", p)
	}
	if err := c.AddToProject(ctx, p, "I_1", "Test"); err != nil {
		t.Fatal(err)
	}
	if len(srv.Items) != 1 || srv.Items[0].IssueNodeID != "I_1" || srv.Items[0].Option != "opt-test" {
		t.Errorf("items = %+v", srv.Items)
	}
	if err := c.AddToProject(ctx, p, "I_1", "Nope"); err == nil || !strings.Contains(err.Error(), `"Nope"`) {
		t.Errorf("AddToProject with an unknown status: err = %v", err)
	}

	if _, err := c.FindProject(ctx, "blanketops", 7); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("FindProject(missing) err = %v", err)
	}
	srv.GraphQLError = "Resource not accessible by integration"
	if _, err := c.FindProject(ctx, "blanketops", 1); err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Errorf("FindProject with a GraphQL error: err = %v", err)
	}
	if err := c.AddToProject(ctx, p, "I_1", "Test"); err == nil {
		t.Error("AddToProject ignored a GraphQL error")
	}
}

func TestAuthAndErrors(t *testing.T) {
	_, srv := newClient(t)
	bad, err := github.New(srv.URL, "wrong", "blanketops/environments-tests")
	if err != nil {
		t.Fatal(err)
	}
	_, err = bad.ListIssues(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("err = %v, want HTTP 401 with the message", err)
	}

	// A non-JSON error body is reported as text; an undecodable success
	// body is an error.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream down"))
			return
		}
		_, _ = w.Write([]byte("not json"))
	}))
	defer plain.Close()
	c, _ := github.New(plain.URL, "", "o/r")
	if err := c.Comment(context.Background(), 1, "x"); err == nil || !strings.Contains(err.Error(), "upstream down") {
		t.Errorf("Comment err = %v", err)
	}
	if _, err := c.ListIssues(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("ListIssues err = %v", err)
	}

	// No server at all.
	plain.Close()
	if _, err := c.ListIssues(context.Background(), "x"); err == nil {
		t.Error("want a connection error")
	}
}
