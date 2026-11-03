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

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blanketops/environments-bots/internal/develop"
)

func runTidy(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("tidy", flag.ContinueOnError)
	var c common
	c.register(fs, getenv)
	branch := fs.String("branch", getenv("GITHUB_REF_NAME"), "the branch to commit fixes on (default $GITHUB_REF_NAME)")
	base := fs.String("base", develop.DevelopBranch, "the branch its pull request targets (to find the PR to comment on)")
	root := fs.String("root", ".", "repository root")
	if code, ok := parse(fs, args, stderr); !ok {
		return code
	}
	if *branch == "" {
		_, _ = fmt.Fprintln(stderr, "developer tidy: no branch: set -branch or $GITHUB_REF_NAME")
		return 2
	}

	// Run the hygiene tools; they write into the (ephemeral) checkout.
	if err := runTool(stderr, *root, "go", "fmt", "./..."); err != nil {
		return fail(stderr, "tidy", err)
	}
	if err := runTool(stderr, *root, "go", "mod", "tidy"); err != nil {
		return fail(stderr, "tidy", err)
	}

	changes, err := detectChanges(*root)
	if err != nil {
		return fail(stderr, "tidy", err)
	}

	api, err := c.client(getenv)
	if err != nil {
		return fail(stderr, "tidy", err)
	}
	ctx, cancel := commandContext()
	defer cancel()
	res, err := develop.Tidy(ctx, api, develop.TidyOptions{Branch: *branch, Base: *base, Changes: changes, DryRun: c.dryRun})
	if err != nil {
		return fail(stderr, "tidy", err)
	}
	if err := c.emit(stdout, develop.TidySummary(res, c.dryRun)); err != nil {
		return fail(stderr, "tidy", err)
	}
	return 0
}

// runTool runs name with args in dir, surfacing its output on failure.
func runTool(stderr io.Writer, dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// detectChanges stages the working tree and returns the files gofmt and go mod
// tidy changed, with the content of each addition.
func detectChanges(root string) ([]develop.TidyChange, error) {
	if out, err := exec.Command("git", "-C", root, "add", "-A").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	changed, err := gitChanged(root, "ACMRT")
	if err != nil {
		return nil, err
	}
	deleted, err := gitChanged(root, "D")
	if err != nil {
		return nil, err
	}
	var changes []develop.TidyChange
	for _, p := range changed {
		content, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		changes = append(changes, develop.TidyChange{Path: p, Content: content})
	}
	for _, p := range deleted {
		changes = append(changes, develop.TidyChange{Path: p, Deleted: true})
	}
	return changes, nil
}

// gitChanged lists the staged paths matching the diff-filter.
func gitChanged(root, filter string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "diff", "--cached", "--name-only", "-z", "--diff-filter="+filter).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff (%s): %w", filter, err)
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
