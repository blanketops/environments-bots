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

// Package clean is the logic behind the environments-cleaner bot: housekeeping
// that keeps a repository's Actions footprint small. Its first task is pruning
// old workflow runs, which otherwise fill the self-hosted runner node's disk.
package clean

import (
	"context"
	"fmt"
	"sort"

	"github.com/blanketops/environments-bots/internal/github"
)

// API is the part of the GitHub client clean uses.
type API interface {
	ListWorkflowRuns(ctx context.Context) ([]github.WorkflowRun, error)
	DeleteWorkflowRun(ctx context.Context, id int64) error
}

// PruneOptions configures PruneRuns.
type PruneOptions struct {
	// Keep is how many of the newest runs to keep per workflow; older
	// completed runs are deleted. Negative is treated as zero.
	Keep int
	// DryRun only reports what it would delete.
	DryRun bool
}

// PruneResult is the outcome of a PruneRuns.
type PruneResult struct {
	Total   int
	Kept    int
	Deleted []github.WorkflowRun
	// Active is the number of not-completed runs left alone even though they
	// are beyond Keep (a run is never deleted while queued or in progress).
	Active int
}

// PruneRuns deletes completed workflow runs beyond the newest Keep per
// workflow, leaving recent history and never touching a run that is still
// queued or in progress. It is safe to repeat.
func PruneRuns(ctx context.Context, api API, opts PruneOptions) (PruneResult, error) {
	keep := opts.Keep
	if keep < 0 {
		keep = 0
	}
	runs, err := api.ListWorkflowRuns(ctx)
	if err != nil {
		return PruneResult{}, fmt.Errorf("list workflow runs: %w", err)
	}
	res := PruneResult{Total: len(runs)}

	byWorkflow := map[int64][]github.WorkflowRun{}
	for _, r := range runs {
		byWorkflow[r.WorkflowID] = append(byWorkflow[r.WorkflowID], r)
	}
	for _, group := range byWorkflow {
		sort.SliceStable(group, func(i, j int) bool { return group[i].CreatedAt > group[j].CreatedAt })
		for i, run := range group {
			if i < keep {
				res.Kept++
				continue
			}
			if !run.Completed() {
				res.Active++
				continue
			}
			res.Deleted = append(res.Deleted, run)
			if opts.DryRun {
				continue
			}
			if err := api.DeleteWorkflowRun(ctx, run.ID); err != nil && !github.IsNotFound(err) {
				return res, fmt.Errorf("delete run %d: %w", run.ID, err)
			}
		}
	}
	return res, nil
}

// PruneSummary is the markdown summary of a PruneRuns.
func PruneSummary(res PruneResult, keep int, dryRun bool) string {
	verb := "Deleted"
	if dryRun {
		verb = "Would delete"
	}
	s := fmt.Sprintf("### Prune workflow runs\n\n%s %d of %d run(s), keeping the newest %d per workflow.\n",
		verb, len(res.Deleted), res.Total, keep)
	if res.Active > 0 {
		s += fmt.Sprintf("\nLeft %d still-running run(s) alone.\n", res.Active)
	}
	return s
}
