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

// Package admin holds the logic of the BlanketOps Environments Admin bot.
// Its first task, Sync, propagates generated manifests (CRDs from
// environments-api, the manager RBAC role from environments-controller) into
// the environments-install repository through a GitHub-signed commit and
// pull request, replacing the raw-token direct push the old sync workflow used.
package admin

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/blanketops/environments-bots/internal/github"
)

// SyncAPI is the part of the GitHub client Sync uses, against the destination
// repository (environments-install).
type SyncAPI interface {
	BranchSHA(ctx context.Context, branch string) (string, error)
	GetContents(ctx context.Context, path string) (string, error)
	CreateBranch(ctx context.Context, branch, sha string) error
	DeleteBranch(ctx context.Context, branch string) error
	CreateCommitOnBranch(ctx context.Context, branch, headline, body, expectedHead string, additions []github.FileAddition, deletions []string) (string, error)
	CreatePull(ctx context.Context, title, head, base, body string) (github.Pull, error)
	MergePull(ctx context.Context, number int) (string, error)
}

// File is one generated manifest to propagate: Name is its basename, Content
// its raw bytes.
type File struct {
	Name    string
	Content string
}

// SyncOptions configures Sync.
type SyncOptions struct {
	// DestPath is the directory in the destination repository the files land
	// in (e.g. "config/crd/bases").
	DestPath string
	// Base is the destination branch synced into (e.g. "main").
	Base string
	// Files are the generated manifests to propagate.
	Files []File
	// Label describes the source for the commit and pull request (e.g.
	// "CRDs from environments-api v0.3.2").
	Label string
	// AutoMerge merges the pull request as soon as it is opened.
	AutoMerge bool
	// DryRun only reports what would change, writing nothing.
	DryRun bool
}

// SyncResult is the outcome of Sync.
type SyncResult struct {
	// Changed are the destination paths that are new or differ from Files.
	Changed []string
	// UpToDate is true when the destination already matches Files.
	UpToDate bool
	// Branch is the sync branch opened (empty on a dry run or when up to date).
	Branch string
	// Pull is the pull request opened (nil on a dry run or when up to date).
	Pull *github.Pull
	// Merged is true when AutoMerge merged the pull request.
	Merged bool
}

// Sync propagates Files into DestPath of the destination repository through a
// GitHub-signed commit on a sync/ branch and a pull request into Base,
// optionally auto-merged. It is a no-op when the destination already matches
// Files, so it is safe to run on every upstream release: the second run sees
// the merged content and does nothing. The branch is named sync/… so the
// destination's open-pr workflow (which ignores sync/**) does not also open a
// duplicate pull request.
func Sync(ctx context.Context, api SyncAPI, opts SyncOptions) (SyncResult, error) {
	if opts.DestPath == "" {
		return SyncResult{}, fmt.Errorf("no dest path")
	}
	if opts.Base == "" {
		return SyncResult{}, fmt.Errorf("no base branch")
	}
	if len(opts.Files) == 0 {
		return SyncResult{}, fmt.Errorf("no files to sync")
	}
	destPath := strings.TrimSuffix(opts.DestPath, "/")

	var res SyncResult
	var changed []File
	for _, f := range opts.Files {
		full := destPath + "/" + f.Name
		cur, err := api.GetContents(ctx, full)
		switch {
		case err == nil && cur == f.Content:
			// already identical in the destination
		case err == nil || github.IsNotFound(err):
			changed = append(changed, f)
			res.Changed = append(res.Changed, full)
		default:
			return res, fmt.Errorf("read %s: %w", full, err)
		}
	}
	sort.Strings(res.Changed)
	if len(changed) == 0 {
		res.UpToDate = true
		return res, nil
	}
	if opts.DryRun {
		return res, nil
	}

	baseSHA, err := api.BranchSHA(ctx, opts.Base)
	if err != nil {
		return res, fmt.Errorf("resolve %s: %w", opts.Base, err)
	}
	branch := "sync/manifests-" + slug(destPath) + "-" + short(baseSHA)
	res.Branch = branch

	// Clear any branch a previous interrupted run left behind so CreateBranch
	// does not fail on an existing ref.
	if err := api.DeleteBranch(ctx, branch); err != nil && !github.IsNotFound(err) {
		return res, fmt.Errorf("clear stale branch %s: %w", branch, err)
	}
	if err := api.CreateBranch(ctx, branch, baseSHA); err != nil {
		return res, fmt.Errorf("create branch %s: %w", branch, err)
	}

	adds := make([]github.FileAddition, len(changed))
	for i, f := range changed {
		adds[i] = github.FileAddition{
			Path:     destPath + "/" + f.Name,
			Contents: base64.StdEncoding.EncodeToString([]byte(f.Content)),
		}
	}
	headline := commitHeadline(opts.Label, destPath)
	if _, err := api.CreateCommitOnBranch(ctx, branch, headline, commitBody(res.Changed), baseSHA, adds, nil); err != nil {
		return res, fmt.Errorf("commit to %s: %w", branch, err)
	}

	pull, err := api.CreatePull(ctx, headline, branch, opts.Base, pullBody(opts.Label, destPath, res.Changed))
	if err != nil {
		return res, fmt.Errorf("open pull request: %w", err)
	}
	res.Pull = &pull

	if opts.AutoMerge {
		if _, err := api.MergePull(ctx, pull.Number); err != nil {
			return res, fmt.Errorf("merge pull request #%d: %w", pull.Number, err)
		}
		res.Merged = true
		if err := api.DeleteBranch(ctx, branch); err != nil && !github.IsNotFound(err) {
			return res, fmt.Errorf("delete merged branch %s: %w", branch, err)
		}
	}
	return res, nil
}

// slug lowercases s and replaces every run of non-alphanumeric characters with
// a single hyphen, so a path makes a valid branch segment.
func slug(s string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
			continue
		}
		if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// short is the first 7 characters of a commit SHA.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func commitHeadline(label, destPath string) string {
	if strings.TrimSpace(label) != "" {
		return "chore(sync): " + label
	}
	return "chore(sync): update " + destPath
}

func commitBody(changed []string) string {
	var b strings.Builder
	b.WriteString("Synced generated manifests from the source repository:\n")
	for _, p := range changed {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	return strings.TrimRight(b.String(), "\n")
}

func pullBody(label, destPath string, changed []string) string {
	var b strings.Builder
	b.WriteString("## What\n\n")
	if strings.TrimSpace(label) != "" {
		fmt.Fprintf(&b, "Sync of %s into `%s`.\n\n", code(label), code(destPath))
	} else {
		fmt.Fprintf(&b, "Sync of generated manifests into `%s`.\n\n", code(destPath))
	}
	b.WriteString("Files:\n")
	for _, p := range changed {
		fmt.Fprintf(&b, "- %s\n", code(p))
	}
	b.WriteString("\n## Why\n\nThe manifests in this directory are generated in the source " +
		"repository and propagated here by the admin bot, so the install manifests " +
		"stay in step with the source.\n\n---\n\nOpened by the admin bot.\n")
	return b.String()
}

// code wraps s in a markdown code span so an untrusted value cannot break the
// markdown or mention anyone.
func code(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}

// SyncSummary is the markdown summary of a Sync.
func SyncSummary(res SyncResult, opts SyncOptions) string {
	switch {
	case res.UpToDate:
		return fmt.Sprintf("### Sync %s\n\n%s is already up to date.\n", opts.DestPath, code(opts.DestPath))
	case opts.DryRun:
		return fmt.Sprintf("### Sync %s ← (dry run)\n\nWould sync %d file(s) into %s on %s:\n%s\n",
			opts.DestPath, len(res.Changed), code(opts.DestPath), code(opts.Base), bullets(res.Changed))
	case res.Pull != nil:
		verb := "Opened"
		if res.Merged {
			verb = "Opened and merged"
		}
		return fmt.Sprintf("### Sync %s\n\n%s pull request [#%d](%s) into %s — %d file(s):\n%s\n",
			opts.DestPath, verb, res.Pull.Number, res.Pull.HTMLURL, code(opts.Base), len(res.Changed), bullets(res.Changed))
	default:
		return fmt.Sprintf("### Sync %s\n\nNothing to do.\n", opts.DestPath)
	}
}

func bullets(items []string) string {
	var b strings.Builder
	for _, i := range items {
		fmt.Fprintf(&b, "- %s\n", code(i))
	}
	return strings.TrimRight(b.String(), "\n")
}
