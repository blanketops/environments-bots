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
	"net/http"
	"strconv"
	"strings"
)

// Merge is a stored server-side merge (POST /merges).
type Merge struct {
	Base, Head, Message string
}

// CommitOnBranch is a stored GraphQL createCommitOnBranch commit.
type CommitOnBranch struct {
	Branch    string
	Headline  string
	Additions []string
	Deletions []string
}

// handleDevelop serves the endpoints developer uses beyond the release
// ones: a server-side merge and deleting a branch. It reports whether it
// handled the request. rest is the path after /repos/owner/repo.
//
// A merge records the merged branch's new head and leaves a request log
// entry; inject a conflict with FailPaths["POST /repos/owner/repo/merges"].
func (s *Server) handleDevelop(w http.ResponseWriter, r *http.Request, rest string) bool {
	switch {
	case r.Method == http.MethodPost && rest == "/merges":
		var in struct{ Base, Head, CommitMessage string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		headSHA, ok := s.Branches[in.Head]
		if !ok {
			// A head given as a raw SHA merges too.
			headSHA = in.Head
		}
		if _, ok := s.Branches[in.Base]; !ok {
			write(w, http.StatusNotFound, map[string]string{"message": "Base does not exist"})
			return true
		}
		s.Merges = append(s.Merges, Merge{Base: in.Base, Head: in.Head, Message: in.CommitMessage})
		merged := "merge-" + headSHA
		s.Branches[in.Base] = merged
		write(w, http.StatusCreated, map[string]any{"sha": merged})
	case r.Method == http.MethodDelete && strings.HasPrefix(rest, "/git/refs/heads/"):
		name := strings.TrimPrefix(rest, "/git/refs/heads/")
		if _, ok := s.Branches[name]; !ok {
			write(w, http.StatusNotFound, map[string]string{"message": "Reference does not exist"})
			return true
		}
		delete(s.Branches, name)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasPrefix(rest, "/issues/") && strings.HasSuffix(rest, "/labels"):
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rest, "/issues/"), "/labels"))
		var in struct{ Labels []string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if is := s.issueByNumber(n); is != nil {
			is.Labels = append(is.Labels, in.Labels...)
			write(w, http.StatusOK, []map[string]string{})
			return true
		}
		for i := range s.Pulls {
			if s.Pulls[i].Number == n {
				s.Pulls[i].Labels = append(s.Pulls[i].Labels, in.Labels...)
				write(w, http.StatusOK, []map[string]string{})
				return true
			}
		}
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "/pulls/"):
		n, err := strconv.Atoi(strings.TrimPrefix(rest, "/pulls/"))
		if err != nil {
			return false // not a bare /pulls/<number>; let other handlers try
		}
		for i := range s.Pulls {
			if s.Pulls[i].Number == n {
				p := s.Pulls[i]
				sha := ""
				if p.Merged {
					sha = "mergecommitsha"
				}
				write(w, http.StatusOK, map[string]any{
					"number": p.Number, "title": p.Title, "state": p.State,
					"html_url":         "https://github.example/" + s.Owner + "/" + s.Repo + "/pull/" + strconv.Itoa(p.Number),
					"merged":           p.Merged,
					"merge_commit_sha": sha,
					"head":             map[string]string{"ref": p.Head},
					"base":             map[string]string{"ref": p.Base},
				})
				return true
			}
		}
		write(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	default:
		return false
	}
	return true
}
