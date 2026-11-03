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

package clean_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/blanketops/environments-bots/internal/clean"
	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/github/githubtest"
)

const token = "t0ken"

// runsServer starts a fake with n completed runs for workflow wf, newest first
// (created_at descending by index).
func runsServer(t *testing.T) (*githubtest.Server, clean.API) {
	t.Helper()
	s := githubtest.New("blanketops", "environments-bots", token)
	t.Cleanup(s.Close)
	api, err := github.New(s.URL, token, "blanketops/environments-bots")
	if err != nil {
		t.Fatalf("github.New: %v", err)
	}
	return s, api
}

func addRuns(s *githubtest.Server, wf int64, statuses ...string) {
	for i, st := range statuses {
		id := wf*1000 + int64(i)
		s.WorkflowRuns = append(s.WorkflowRuns, githubtest.WorkflowRun{
			ID: id, WorkflowID: wf, Status: st,
			// newest first: earlier index = larger timestamp
			CreatedAt: fmt.Sprintf("2026-11-%02dT00:00:00Z", 28-i),
		})
	}
}

func TestPruneKeepsNewestPerWorkflow(t *testing.T) {
	s, api := runsServer(t)
	// Workflow 1: 5 completed runs; keep 2 -> delete 3.
	addRuns(s, 1, "completed", "completed", "completed", "completed", "completed")
	// Workflow 2: 1 completed run; keep 2 -> delete 0.
	addRuns(s, 2, "completed")
	res, err := clean.PruneRuns(context.Background(), api, clean.PruneOptions{Keep: 2})
	if err != nil {
		t.Fatalf("PruneRuns: %v", err)
	}
	if len(res.Deleted) != 3 {
		t.Fatalf("want 3 deleted, got %d", len(res.Deleted))
	}
	// The two newest of workflow 1 and the single run of workflow 2 remain.
	if len(s.WorkflowRuns) != 3 {
		t.Fatalf("want 3 runs left, got %d", len(s.WorkflowRuns))
	}
	// The newest of workflow 1 (id 1000) must survive.
	kept := map[int64]bool{}
	for _, r := range s.WorkflowRuns {
		kept[r.ID] = true
	}
	if !kept[1000] || !kept[1001] || !kept[2000] {
		t.Fatalf("wrong runs kept: %+v", s.WorkflowRuns)
	}
}

func TestPruneNeverDeletesActive(t *testing.T) {
	s, api := runsServer(t)
	// Newest is in_progress, then 3 completed; keep 1.
	addRuns(s, 1, "in_progress", "completed", "completed", "completed")
	res, err := clean.PruneRuns(context.Background(), api, clean.PruneOptions{Keep: 1})
	if err != nil {
		t.Fatalf("PruneRuns: %v", err)
	}
	// Keep the in_progress (newest), delete the 3 completed beyond it.
	if len(res.Deleted) != 3 {
		t.Fatalf("want 3 deleted, got %d", len(res.Deleted))
	}
	for _, r := range res.Deleted {
		if r.Status != "completed" {
			t.Fatalf("deleted a non-completed run: %+v", r)
		}
	}
}

func TestPruneSkipsActiveBeyondKeep(t *testing.T) {
	s, api := runsServer(t)
	// keep 0; newest is in_progress (must be left), then 2 completed (deleted).
	addRuns(s, 1, "in_progress", "completed", "completed")
	res, err := clean.PruneRuns(context.Background(), api, clean.PruneOptions{Keep: 0})
	if err != nil {
		t.Fatalf("PruneRuns: %v", err)
	}
	if len(res.Deleted) != 2 || res.Active != 1 {
		t.Fatalf("want 2 deleted and 1 active, got deleted=%d active=%d", len(res.Deleted), res.Active)
	}
	if s.Run(1000) == nil {
		t.Fatal("in_progress run was deleted")
	}
}

func TestPruneDryRun(t *testing.T) {
	s, api := runsServer(t)
	addRuns(s, 1, "completed", "completed", "completed")
	res, err := clean.PruneRuns(context.Background(), api, clean.PruneOptions{Keep: 1, DryRun: true})
	if err != nil {
		t.Fatalf("PruneRuns: %v", err)
	}
	if len(res.Deleted) != 2 {
		t.Fatalf("want 2 would-delete, got %d", len(res.Deleted))
	}
	if len(s.WorkflowRuns) != 3 {
		t.Fatalf("dry run deleted runs: %d left", len(s.WorkflowRuns))
	}
}
