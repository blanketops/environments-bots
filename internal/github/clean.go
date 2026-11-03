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

package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// This file holds the calls cleaner makes: listing and deleting workflow runs.

// WorkflowRun is the part of an Actions workflow run cleaner needs.
type WorkflowRun struct {
	ID         int64  `json:"id"`
	WorkflowID int64  `json:"workflow_id"`
	Name       string `json:"name"`
	Status     string `json:"status"` // queued, in_progress, completed
	Conclusion string `json:"conclusion"`
	CreatedAt  string `json:"created_at"`
	HeadBranch string `json:"head_branch"`
	Event      string `json:"event"`
}

// Completed reports whether the run has finished (and so is safe to delete).
func (r WorkflowRun) Completed() bool { return r.Status == "completed" }

// ListWorkflowRuns returns every workflow run of the repository, newest first,
// following the Link header's pages.
func (c *Client) ListWorkflowRuns(ctx context.Context) ([]WorkflowRun, error) {
	var all []WorkflowRun
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("per_page", "100")
		q.Set("page", fmt.Sprint(page))
		var out struct {
			WorkflowRuns []WorkflowRun `json:"workflow_runs"`
		}
		h, err := c.doHeader(ctx, http.MethodGet, c.repoPath("/actions/runs?"+q.Encode()), nil, &out)
		if err != nil {
			return nil, err
		}
		all = append(all, out.WorkflowRuns...)
		if !hasNext(h) || len(out.WorkflowRuns) == 0 {
			return all, nil
		}
	}
}

// DeleteWorkflowRun deletes a workflow run. IsNotFound(err) when it is already
// gone.
func (c *Client) DeleteWorkflowRun(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, c.repoPath(fmt.Sprintf("/actions/runs/%d", id)), nil, nil)
}
