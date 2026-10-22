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

// Command test-helper keeps one GitHub issue per test file of a repository
// and posts each test run's results to it.
//
// A test file is a *_test.go file under tests/ (tests/data excluded) that
// declares Go test functions or top-level Ginkgo containers. Its issue is
// titled "[test] <path under tests/ without .go>" and labelled
// test-tracking.
//
//	test-helper sync     open an issue for every test file that has none
//	test-helper report   comment each file's results on its issue
//
// GitHub is reached with the token in GH_TOKEN (or GITHUB_TOKEN), normally
// an installation token of the BlanketOps-Environments App.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blanketops/environments-bots/internal/discover"
	"github.com/blanketops/environments-bots/internal/github"
	"github.com/blanketops/environments-bots/internal/results"
	"github.com/blanketops/environments-bots/internal/tracker"
)

// version is set at release time with
// -ldflags "-X main.version=<tag>".
var version = "dev"

const usage = `test-helper keeps one GitHub issue per test file and posts each run's results to it.

Usage:
  test-helper <command> [flags]

Commands:
  sync      Open an issue for every test file under tests/ that has none
  report    Comment each test file's results on its issue
  version   Print the version
  help      Print this help

Run "test-helper <command> -h" for a command's flags. GitHub is reached
with the token in GH_TOKEN (or GITHUB_TOKEN).
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
	case "report":
		return runReport(args[1:], stdout, stderr, getenv)
	default:
		_, _ = fmt.Fprintf(stderr, "test-helper: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// common are the flags both commands share.
type common struct {
	root, repo, apiURL, summary string
	dryRun                      bool
}

func (c *common) register(fs *flag.FlagSet, getenv func(string) string) {
	fs.StringVar(&c.root, "root", ".", "repository root")
	fs.StringVar(&c.repo, "repo", getenv("GITHUB_REPOSITORY"), "repository as owner/name (default $GITHUB_REPOSITORY)")
	fs.StringVar(&c.apiURL, "api-url", getenv("GITHUB_API_URL"), "GitHub API URL (default $GITHUB_API_URL, else https://api.github.com)")
	fs.StringVar(&c.summary, "summary", getenv("GITHUB_STEP_SUMMARY"), "file to append the markdown summary to (default $GITHUB_STEP_SUMMARY)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "read only: show what would be created or posted")
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

func (c *common) discover() ([]discover.File, error) {
	return discover.Discover(c.root, discover.Options{})
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
		_, _ = fmt.Fprintf(stderr, "test-helper %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return 2, false
	}
	return 0, true
}

func fail(stderr io.Writer, cmd string, err error) int {
	_, _ = fmt.Fprintf(stderr, "test-helper %s: %v\n", cmd, err)
	return 1
}

func commandContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}

func runSync(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	var c common
	c.register(fs, getenv)
	project := fs.String("project", "", "add new issues to this organization project, as org/number (e.g. blanketops/1)")
	status := fs.String("status", "Test", "Status of new issues on the project")
	source := fs.String("source", "", "shown in new issues as where they were opened from (e.g. the pull request URL)")
	if code, ok := parse(fs, args, stderr); !ok {
		return code
	}

	opts := tracker.SyncOptions{Source: *source, DryRun: c.dryRun}
	if *project != "" {
		ref, err := parseProject(*project, *status)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "test-helper sync: %v\n", err)
			return 2
		}
		opts.Project = &ref
	}
	api, err := c.client(getenv)
	if err != nil {
		return fail(stderr, "sync", err)
	}
	if opts.Files, err = c.discover(); err != nil {
		return fail(stderr, "sync", err)
	}
	ctx, cancel := commandContext()
	defer cancel()
	res, err := tracker.Sync(ctx, api, opts)
	if err != nil {
		return fail(stderr, "sync", err)
	}
	if err := c.emit(stdout, tracker.SyncSummary(res, c.dryRun)); err != nil {
		return fail(stderr, "sync", err)
	}
	for _, w := range res.Warnings {
		_, _ = fmt.Fprintf(stderr, "test-helper sync: warning: %s\n", w)
	}
	return 0
}

func parseProject(s, status string) (tracker.ProjectRef, error) {
	org, num, ok := strings.Cut(s, "/")
	n, err := strconv.Atoi(num)
	if !ok || org == "" || err != nil || n <= 0 {
		return tracker.ProjectRef{}, fmt.Errorf("-project %q is not org/number", s)
	}
	return tracker.ProjectRef{Org: org, Number: n, Status: status}, nil
}

// files is a repeatable string flag.
type files []string

func (f *files) String() string     { return strings.Join(*f, ",") }
func (f *files) Set(v string) error { *f = append(*f, v); return nil }

func runReport(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	var c common
	c.register(fs, getenv)
	var goJSON, ginkgoJSON files
	fs.Var(&goJSON, "go-json", "`go test -json` output file (repeatable)")
	fs.Var(&ginkgoJSON, "ginkgo-json", "Ginkgo JSON report file (repeatable)")
	var info tracker.RunInfo
	fs.StringVar(&info.URL, "run-url", runURL(getenv), "link to the run (default from $GITHUB_SERVER_URL, $GITHUB_REPOSITORY, $GITHUB_RUN_ID)")
	fs.StringVar(&info.Name, "run-name", runName(getenv), "label of the run link (default \"$GITHUB_WORKFLOW #$GITHUB_RUN_NUMBER\")")
	fs.StringVar(&info.SHA, "sha", getenv("GITHUB_SHA"), "tested commit (default $GITHUB_SHA)")
	fs.StringVar(&info.Ref, "ref", "", "tested branch or pull request, e.g. \"PR #53\"")
	if code, ok := parse(fs, args, stderr); !ok {
		return code
	}
	if len(goJSON) == 0 && len(ginkgoJSON) == 0 {
		_, _ = fmt.Fprintln(stderr, "test-helper report: give at least one -go-json or -ginkgo-json file")
		return 2
	}

	api, err := c.client(getenv)
	if err != nil {
		return fail(stderr, "report", err)
	}
	found, err := c.discover()
	if err != nil {
		return fail(stderr, "report", err)
	}
	module, err := modulePath(c.root)
	if err != nil {
		return fail(stderr, "report", err)
	}
	root, err := filepath.Abs(c.root)
	if err != nil {
		return fail(stderr, "report", err)
	}

	runRes := results.NewRun()
	for _, p := range goJSON {
		if err := readInto(p, runRes.AddGoTestJSON); err != nil {
			return fail(stderr, "report", err)
		}
	}
	for _, p := range ginkgoJSON {
		if err := readInto(p, runRes.AddGinkgoJSON); err != nil {
			return fail(stderr, "report", err)
		}
	}

	ctx, cancel := commandContext()
	defer cancel()
	res, err := tracker.Report(ctx, api, tracker.ReportOptions{
		Results: runRes.Assign(found, module, root),
		Run:     info,
		DryRun:  c.dryRun,
	})
	if err != nil {
		return fail(stderr, "report", err)
	}
	if err := c.emit(stdout, tracker.ReportSummary(res, c.dryRun)); err != nil {
		return fail(stderr, "report", err)
	}
	return 0
}

func readInto(path string, add func(io.Reader) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := add(f); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func runURL(getenv func(string) string) string {
	server, repo, id := getenv("GITHUB_SERVER_URL"), getenv("GITHUB_REPOSITORY"), getenv("GITHUB_RUN_ID")
	if server == "" || repo == "" || id == "" {
		return ""
	}
	return server + "/" + repo + "/actions/runs/" + id
}

func runName(getenv func(string) string) string {
	name := getenv("GITHUB_WORKFLOW")
	if n := getenv("GITHUB_RUN_NUMBER"); name != "" && n != "" {
		return name + " #" + n
	}
	return name
}

// modulePath reads the module path from root's go.mod.
func modulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module"); ok && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", fmt.Errorf("%s: no module line", filepath.Join(root, "go.mod"))
}
