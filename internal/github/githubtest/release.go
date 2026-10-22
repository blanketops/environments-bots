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

package githubtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Tag is a stored tag.
type Tag struct{ Name, SHA string }

// Commit is a stored commit.
type Commit struct{ SHA, Message string }

// Release is a stored, published release.
type Release struct {
	Name       string
	Prerelease bool
}

// Milestone is a stored milestone.
type Milestone struct {
	Number int
	Title  string
	State  string
}

// Pull is a stored pull request.
type Pull struct {
	Number     int
	Title      string
	State      string
	Base, Head string
	Merged     bool
}

// AddMilestone stores an open milestone and returns its number.
func (s *Server) AddMilestone(title string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := &Milestone{Number: len(s.Milestones) + 1, Title: title, State: "open"}
	s.Milestones = append(s.Milestones, m)
	return m.Number
}

func commitsJSON(cs []Commit) []map[string]any {
	out := []map[string]any{}
	for _, c := range cs {
		out = append(out, map[string]any{"sha": c.SHA, "commit": map[string]string{"message": c.Message}})
	}
	return out
}

func (s *Server) issueByNumber(n int) *Issue {
	for _, is := range s.Issues {
		if is.Number == n {
			return is
		}
	}
	return nil
}

// handleRelease serves the release endpoints; it reports whether it
// handled the request. rest is the path after /repos/owner/repo.
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request, rest string) bool {
	get := r.Method == http.MethodGet
	notFound := func() { write(w, http.StatusNotFound, map[string]string{"message": "Not Found"}) }
	switch {
	case get && rest == "/tags":
		out := []map[string]string{}
		for _, t := range s.Tags {
			out = append(out, map[string]string{"name": t.Name})
		}
		write(w, http.StatusOK, out)
	case get && strings.HasPrefix(rest, "/compare/"):
		cs, ok := s.Compares[strings.TrimPrefix(rest, "/compare/")]
		if !ok {
			notFound()
			return true
		}
		write(w, http.StatusOK, map[string]any{"commits": commitsJSON(cs)})
	case get && rest == "/commits":
		cs, ok := s.RefCommits[r.URL.Query().Get("sha")]
		if !ok {
			notFound()
			return true
		}
		write(w, http.StatusOK, commitsJSON(cs))
	case get && strings.HasPrefix(rest, "/git/ref/tags/"):
		name := strings.TrimPrefix(rest, "/git/ref/tags/")
		for _, t := range s.Tags {
			if t.Name == name {
				write(w, http.StatusOK, map[string]any{"ref": "refs/tags/" + name, "object": map[string]string{"sha": t.SHA}})
				return true
			}
		}
		notFound()
	case get && strings.HasPrefix(rest, "/releases/tags/"):
		tag := strings.TrimPrefix(rest, "/releases/tags/")
		rel, ok := s.Releases[tag]
		if !ok {
			notFound()
			return true
		}
		write(w, http.StatusOK, map[string]any{"tag_name": tag, "name": rel.Name, "prerelease": rel.Prerelease, "draft": false,
			"html_url": fmt.Sprintf("https://github.example/%s/%s/releases/tag/%s", s.Owner, s.Repo, tag)})
	case get && rest == "/milestones":
		out := []map[string]any{}
		for _, m := range s.Milestones {
			open := 0
			for _, is := range s.Issues {
				if is.Milestone == m.Number && is.State == "open" {
					open++
				}
			}
			out = append(out, map[string]any{"number": m.Number, "title": m.Title, "state": m.State, "open_issues": open,
				"html_url": fmt.Sprintf("https://github.example/%s/%s/milestone/%d", s.Owner, s.Repo, m.Number)})
		}
		write(w, http.StatusOK, out)
	case r.Method == http.MethodPatch && strings.HasPrefix(rest, "/milestones/"):
		n, _ := strconv.Atoi(strings.TrimPrefix(rest, "/milestones/"))
		var in struct{ State string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, m := range s.Milestones {
			if m.Number == n {
				m.State = in.State
				write(w, http.StatusOK, map[string]any{"number": n, "state": m.State})
				return true
			}
		}
		notFound()
	case r.Method == http.MethodPatch && strings.HasPrefix(rest, "/issues/"):
		n, _ := strconv.Atoi(strings.TrimPrefix(rest, "/issues/"))
		is := s.issueByNumber(n)
		if is == nil {
			notFound()
			return true
		}
		var in struct{ State string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		is.State = in.State
		write(w, http.StatusOK, s.issueJSON(is))
	case get && rest == "/pulls":
		q := r.URL.Query()
		out := []map[string]any{}
		for _, p := range s.Pulls {
			if q.Get("state") != "all" && q.Get("state") != p.State || q.Get("base") != p.Base ||
				q.Get("head") != "" && q.Get("head") != s.Owner+":"+p.Head {
				continue
			}
			pr := map[string]any{"number": p.Number, "title": p.Title, "state": p.State, "merged_at": nil,
				"html_url": fmt.Sprintf("https://github.example/%s/%s/pull/%d", s.Owner, s.Repo, p.Number)}
			if p.Merged {
				pr["merged_at"] = "2026-10-22T12:00:00Z"
			}
			out = append(out, pr)
		}
		write(w, http.StatusOK, out)
	default:
		return false
	}
	return true
}
