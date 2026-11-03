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

// Command developer keeps a repository's branches in order.
//
//	developer sync    open a pull request merging main into develop when
//	                  main is ahead
//	developer open    open a pull request and a tracking issue for a branch
//	                  on its first push
//	developer resolve close a branch's tracking issue when its pull request
//	                  merges
//	developer tidy    commit gofmt and go mod tidy fixes to a branch
//
// GitHub is reached with the token in GH_TOKEN (or GITHUB_TOKEN), normally
// an installation token of the BlanketOps Environments Developer App.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blanketops/environments-bots/internal/develop"
	"github.com/blanketops/environments-bots/internal/github"
)

// version is set at release time with
// -ldflags "-X main.version=<tag>".
var version = "dev"

const usage = `developer keeps a repository's branches in order.

Usage:
  developer <command> [flags]

Commands:
  sync      Open a pull request merging main into develop when main is ahead
  open      Open a pull request and a tracking issue for a branch on its first push
  resolve   Close a branch's tracking issue when its pull request merges
  tidy      Commit gofmt and go mod tidy fixes to a branch
  version   Print the version
  help      Print this help

Run "developer <command> -h" for a command's flags. GitHub is reached with
the token in GH_TOKEN (or GITHUB_TOKEN).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run executes the command in args and returns the process exit code:
// 0 on success, 1 when the command failed, 2 on a usage error.
func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-v":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	case "help", "--help", "-h":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "sync":
		return runSync(args[1:], stdout, stderr, getenv)
	case "open":
		return runOpen(args[1:], stdout, stderr, getenv)
	case "resolve":
		return runResolve(args[1:], stdout, stderr, getenv)
	case "tidy":
		return runTidy(args[1:], stdout, stderr, getenv)
	default:
		_, _ = fmt.Fprintf(stderr, "developer: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// common are the flags every command shares.
type common struct {
	repo, apiURL, summary string
	dryRun                bool
}

func (c *common) register(fs *flag.FlagSet, getenv func(string) string) {
	fs.StringVar(&c.repo, "repo", getenv("GITHUB_REPOSITORY"), "repository as owner/name (default $GITHUB_REPOSITORY)")
	fs.StringVar(&c.apiURL, "api-url", getenv("GITHUB_API_URL"), "GitHub API URL (default $GITHUB_API_URL, else https://api.github.com)")
	fs.StringVar(&c.summary, "summary", getenv("GITHUB_STEP_SUMMARY"), "file to append the markdown summary to (default $GITHUB_STEP_SUMMARY)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "read only: show what would be done")
}

// client builds the API client. A token is required unless this is a dry
// run, which only reads (public repositories need none).
func (c *common) client(getenv func(string) string) (*github.Client, error) {
	token := getenv("GH_TOKEN")
	if token == "" {
		token = getenv("GITHUB_TOKEN")
	}
	if token == "" && !c.dryRun {
		return nil, errors.New("no token: set GH_TOKEN (or GITHUB_TOKEN), or use -dry-run")
	}
	return github.New(c.apiURL, token, c.repo)
}

// emit prints the summary and appends it to the summary file, if any.
func (c *common) emit(stdout io.Writer, md string) error {
	_, _ = fmt.Fprint(stdout, md)
	if c.summary == "" {
		return nil
	}
	f, err := os.OpenFile(c.summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, md); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// parse parses a command's flags. It returns the exit code to stop with,
// or ok to continue.
func parse(fs *flag.FlagSet, args []string, stderr io.Writer) (int, bool) {
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "developer %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return 2, false
	}
	return 0, true
}

func fail(stderr io.Writer, cmd string, err error) int {
	_, _ = fmt.Fprintf(stderr, "developer %s: %v\n", cmd, err)
	return 1
}

func commandContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}

func runSync(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	var c common
	c.register(fs, getenv)
	base := fs.String("base", develop.DevelopBranch, "branch kept up to date")
	from := fs.String("from", develop.MainBranch, "branch merged into base")
	if code, ok := parse(fs, args, stderr); !ok {
		return code
	}

	api, err := c.client(getenv)
	if err != nil {
		return fail(stderr, "sync", err)
	}
	ctx, cancel := commandContext()
	defer cancel()
	res, err := develop.Sync(ctx, api, develop.SyncOptions{Base: *base, From: *from, DryRun: c.dryRun})
	if err != nil {
		return fail(stderr, "sync", err)
	}
	if err := c.emit(stdout, develop.SyncSummary(res, c.dryRun)); err != nil {
		return fail(stderr, "sync", err)
	}
	return 0
}

func runOpen(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	var c common
	c.register(fs, getenv)
	branch := fs.String("branch", getenv("GITHUB_REF_NAME"), "the pushed branch (default $GITHUB_REF_NAME)")
	base := fs.String("base", develop.DevelopBranch, "base branch for the pull request")
	if code, ok := parse(fs, args, stderr); !ok {
		return code
	}
	if *branch == "" {
		_, _ = fmt.Fprintln(stderr, "developer open: no branch: set -branch or $GITHUB_REF_NAME")
		return 2
	}

	api, err := c.client(getenv)
	if err != nil {
		return fail(stderr, "open", err)
	}
	ctx, cancel := commandContext()
	defer cancel()
	res, err := develop.Open(ctx, api, develop.OpenOptions{Branch: *branch, Base: *base, DryRun: c.dryRun})
	if err != nil {
		return fail(stderr, "open", err)
	}
	if err := c.emit(stdout, develop.OpenSummary(res, c.dryRun)); err != nil {
		return fail(stderr, "open", err)
	}
	return 0
}

func runResolve(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	var c common
	c.register(fs, getenv)
	pull := fs.Int("pull", pullFromRef(getenv("GITHUB_REF")), "the merged pull request number (default parsed from $GITHUB_REF)")
	if code, ok := parse(fs, args, stderr); !ok {
		return code
	}
	if *pull <= 0 {
		_, _ = fmt.Fprintln(stderr, "developer resolve: no pull request: set -pull or run on a pull_request event")
		return 2
	}

	api, err := c.client(getenv)
	if err != nil {
		return fail(stderr, "resolve", err)
	}
	ctx, cancel := commandContext()
	defer cancel()
	res, err := develop.Resolve(ctx, api, develop.ResolveOptions{Pull: *pull, DryRun: c.dryRun})
	if err != nil {
		return fail(stderr, "resolve", err)
	}
	if err := c.emit(stdout, develop.ResolveSummary(res, c.dryRun)); err != nil {
		return fail(stderr, "resolve", err)
	}
	return 0
}

// pullFromRef parses the pull request number from a refs/pull/<n>/merge ref,
// or returns 0.
func pullFromRef(ref string) int {
	rest, ok := strings.CutPrefix(ref, "refs/pull/")
	if !ok {
		return 0
	}
	num, _, _ := strings.Cut(rest, "/")
	n, _ := strconv.Atoi(num)
	return n
}
