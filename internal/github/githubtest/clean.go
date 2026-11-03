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
	"net/http"
	"strconv"
	"strings"
)

// WorkflowRun is a stored Actions workflow run.
type WorkflowRun struct {
	ID         int64
	WorkflowID int64
	Name       string
	Status     string
	Conclusion string
	CreatedAt  string
}

// Run returns the stored workflow run with id, or nil.
func (s *Server) Run(id int64) *WorkflowRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.WorkflowRuns {
		if s.WorkflowRuns[i].ID == id {
			return &s.WorkflowRuns[i]
		}
	}
	return nil
}

// handleClean serves the workflow-run endpoints cleaner uses. It reports
// whether it handled the request. rest is the path after /repos/owner/repo.
func (s *Server) handleClean(w http.ResponseWriter, r *http.Request, rest string) bool {
	switch {
	case r.Method == http.MethodGet && rest == "/actions/runs":
		out := []map[string]any{}
		for _, run := range s.WorkflowRuns {
			out = append(out, map[string]any{
				"id": run.ID, "workflow_id": run.WorkflowID, "name": run.Name,
				"status": run.Status, "conclusion": run.Conclusion, "created_at": run.CreatedAt,
			})
		}
		write(w, http.StatusOK, map[string]any{"total_count": len(out), "workflow_runs": out})
	case r.Method == http.MethodDelete && strings.HasPrefix(rest, "/actions/runs/"):
		id, err := strconv.ParseInt(strings.TrimPrefix(rest, "/actions/runs/"), 10, 64)
		if err != nil {
			return false
		}
		kept := s.WorkflowRuns[:0]
		found := false
		for _, run := range s.WorkflowRuns {
			if run.ID == id {
				found = true
				continue
			}
			kept = append(kept, run)
		}
		s.WorkflowRuns = kept
		if !found {
			write(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}
