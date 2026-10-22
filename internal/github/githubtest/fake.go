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

// Package githubtest is an in-memory fake of the GitHub API endpoints the
// github package calls, for tests.
package githubtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// Issue is a stored issue.
type Issue struct {
	Number   int
	Title    string
	Body     string
	State    string
	Labels   []string
	Comments []string
	// PR marks a pull request, which the issues list also returns.
	PR bool
	// Milestone is the number of the issue's milestone; 0 for none.
	Milestone int
	// Author is the login of whoever opened it; issues created through
	// the API get the server's Login.
	Author string
}

// ProjectItem is an issue added to the fake project.
type ProjectItem struct {
	IssueNodeID string
	Option      string
}

// Server is the fake.
type Server struct {
	*httptest.Server

	mu     sync.Mutex
	Owner  string
	Repo   string
	Token  string
	Issues []*Issue
	Labels map[string]bool
	// Org and ProjectNumber identify the one project; StatusOptions are its
	// Status options (name -> ID).
	Org           string
	ProjectNumber int
	StatusOptions map[string]string
	Items         []ProjectItem
	// FailPaths makes requests whose "METHOD path" or "METHOD path?query"
	// starts with a key fail with that status.
	FailPaths map[string]int
	// GraphQLError, if set, is returned as a GraphQL error for every query.
	GraphQLError string
	// Requests logs "METHOD path" of every request.
	Requests []string
	// PerPage overrides the page size the fake uses for issue lists (the
	// client always asks for 100); 0 keeps the request's per_page.
	PerPage int

	// Release data; see release.go.
	Tags       []Tag
	Compares   map[string][]Commit // "base...head" -> commits, oldest first
	RefCommits map[string][]Commit // ref -> commits, newest first
	Releases   map[string]Release  // tag -> published release
	Milestones []*Milestone
	Pulls      []Pull
	Branches   map[string]string // branch -> commit
	// Login is the author of issues created through the API (the App\'s bot).
	Login string
}

// New starts a fake for owner/repo that requires token (empty: any).
func New(owner, repo, token string) *Server {
	s := &Server{
		Owner: owner, Repo: repo, Token: token,
		Labels:        map[string]bool{},
		Org:           owner,
		ProjectNumber: 1,
		StatusOptions: map[string]string{"Backlog": "opt-backlog", "Test": "opt-test"},
		FailPaths:     map[string]int{},
		Compares:      map[string][]Commit{},
		RefCommits:    map[string][]Commit{},
		Releases:      map[string]Release{},
		Branches:      map[string]string{},
		Login:         "blanketops-environments-releaser[bot]",
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// Issue returns the stored issue with the given title, or nil.
func (s *Server) Issue(title string) *Issue {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, is := range s.Issues {
		if is.Title == title {
			return is
		}
	}
	return nil
}

// AddIssue stores an issue and returns its number.
func (s *Server) AddIssue(is Issue) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	is.Number = len(s.Issues) + 1
	if is.State == "" {
		is.State = "open"
	}
	s.Issues = append(s.Issues, &is)
	return is.Number
}

func nodeID(n int) string { return fmt.Sprintf("I_%d", n) }

func (s *Server) issueJSON(is *Issue) map[string]any {
	m := map[string]any{
		"number": is.Number, "title": is.Title, "state": is.State,
		"node_id": nodeID(is.Number), "html_url": fmt.Sprintf("https://github.example/%s/%s/issues/%d", s.Owner, s.Repo, is.Number),
		"user": map[string]string{"login": is.Author},
	}
	if is.PR {
		m["pull_request"] = map[string]any{}
	}
	return m
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	s.Requests = append(s.Requests, key)
	withQuery := r.Method + " " + r.URL.RequestURI()
	for prefix, status := range s.FailPaths {
		if strings.HasPrefix(key, prefix) || strings.HasPrefix(withQuery, prefix) {
			write(w, status, map[string]string{"message": "injected failure"})
			return
		}
	}
	if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
		write(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	if r.URL.Path == "/graphql" && r.Method == http.MethodPost {
		s.graphql(w, r)
		return
	}
	base := "/repos/" + s.Owner + "/" + s.Repo
	rest, ok := strings.CutPrefix(r.URL.Path, base)
	if !ok {
		write(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	switch {
	case r.Method == http.MethodGet && rest == "/issues":
		s.listIssues(w, r.URL.Query())
	case r.Method == http.MethodPost && rest == "/issues":
		var in struct {
			Title, Body string
			Labels      []string
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		is := &Issue{Number: len(s.Issues) + 1, Title: in.Title, Body: in.Body, State: "open", Labels: in.Labels, Author: s.Login}
		s.Issues = append(s.Issues, is)
		write(w, http.StatusCreated, s.issueJSON(is))
	case r.Method == http.MethodPost && strings.HasPrefix(rest, "/issues/") && strings.HasSuffix(rest, "/comments"):
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rest, "/issues/"), "/comments"))
		var in struct{ Body string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, is := range s.Issues {
			if is.Number == n {
				is.Comments = append(is.Comments, in.Body)
				write(w, http.StatusCreated, map[string]any{"id": len(is.Comments)})
				return
			}
		}
		write(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "/labels/"):
		name, _ := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/repos/"+s.Owner+"/"+s.Repo+"/labels/"))
		if s.Labels[name] {
			write(w, http.StatusOK, map[string]string{"name": name})
		} else {
			write(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
	case r.Method == http.MethodPost && rest == "/labels":
		var in struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.Labels[in.Name] = true
		write(w, http.StatusCreated, map[string]string{"name": in.Name})
	case s.handleRelease(w, r, rest):
	default:
		write(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

func (s *Server) listIssues(w http.ResponseWriter, q url.Values) {
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	if s.PerPage > 0 {
		perPage = s.PerPage
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	label := q.Get("labels")
	var matched []map[string]any
	for _, is := range s.Issues {
		if q.Get("state") != "all" && is.State != "open" {
			continue
		}
		if m := q.Get("milestone"); m != "" && strconv.Itoa(is.Milestone) != m {
			continue
		}
		has := label == ""
		for _, l := range is.Labels {
			if l == label {
				has = true
			}
		}
		if has {
			matched = append(matched, s.issueJSON(is))
		}
	}
	start := (page - 1) * perPage
	if start > len(matched) {
		start = len(matched)
	}
	end := start + perPage
	if end > len(matched) {
		end = len(matched)
	}
	if end < len(matched) {
		w.Header().Set("Link", fmt.Sprintf(`<%s?page=%d>; rel="next", <%s?page=1>; rel="first"`, s.URL, page+1, s.URL))
	}
	write(w, http.StatusOK, matched[start:end])
}

func (s *Server) graphql(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Query     string
		Variables map[string]any
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if s.GraphQLError != "" {
		write(w, http.StatusOK, map[string]any{"errors": []map[string]string{{"message": s.GraphQLError}}})
		return
	}
	switch {
	case strings.Contains(in.Query, "projectV2(number"):
		var project any
		if in.Variables["org"] == s.Org && in.Variables["number"] == float64(s.ProjectNumber) {
			var opts []map[string]string
			for name, id := range s.StatusOptions {
				opts = append(opts, map[string]string{"id": id, "name": name})
			}
			project = map[string]any{"id": "PVT_1", "field": map[string]any{"id": "FIELD_status", "options": opts}}
		}
		write(w, http.StatusOK, map[string]any{"data": map[string]any{"organization": map[string]any{"projectV2": project}}})
	case strings.Contains(in.Query, "addProjectV2ItemById"):
		content, _ := in.Variables["content"].(string)
		s.Items = append(s.Items, ProjectItem{IssueNodeID: content})
		write(w, http.StatusOK, map[string]any{"data": map[string]any{"addProjectV2ItemById": map[string]any{"item": map[string]string{"id": fmt.Sprintf("ITEM_%d", len(s.Items))}}}})
	case strings.Contains(in.Query, "updateProjectV2ItemFieldValue"):
		item, _ := in.Variables["item"].(string)
		n, _ := strconv.Atoi(strings.TrimPrefix(item, "ITEM_"))
		if n >= 1 && n <= len(s.Items) {
			s.Items[n-1].Option, _ = in.Variables["option"].(string)
		}
		write(w, http.StatusOK, map[string]any{"data": map[string]any{"updateProjectV2ItemFieldValue": map[string]any{"projectV2Item": map[string]string{"id": item}}}})
	default:
		write(w, http.StatusOK, map[string]any{"errors": []map[string]string{{"message": "unknown query"}}})
	}
}
