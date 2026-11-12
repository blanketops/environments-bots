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

// Command admin keeps generated manifests in sync across repositories — the
// environments-admin bot. Its first task propagates generated manifests (the
// CRDs built in environments-api, the manager RBAC role built in
// environments-controller) into the environments-install repository through a
// GitHub-signed pull request.
//
//	admin sync   propagate generated manifests into a destination repository
//
// GitHub is reached with the token in GH_TOKEN (or GITHUB_TOKEN), normally an
// installation token of the BlanketOps Environments Admin App scoped to the
// destination repository.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blanketops/environments-bots/internal/admin"
	"github.com/blanketops/environments-bots/internal/github"
)

// version is set at release time with -ldflags "-X main.version=<tag>".
var version = "dev"

const usage = `admin keeps generated manifests in sync across repositories.

Usage:
  admin <command> [flags]

Commands:
  sync      Propagate generated manifests into a destination repository
  version   Print the version
  help      Print this help

Run "admin <command> -h" for a command's flags. GitHub is reached with the
token in GH_TOKEN (or GITHUB_TOKEN).
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
	default:
		_, _ = fmt.Fprintf(stderr, "admin: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// common are the flags every command shares.
type common struct {
	repo, apiURL, summary string
	dryRun                bool
}

func (c *common) register(fs *flag.FlagSet, getenv func(string) string) {
	fs.StringVar(&c.repo, "repo", getenv("GITHUB_REPOSITORY"), "destination repository as owner/name (default $GITHUB_REPOSITORY)")
	fs.StringVar(&c.apiURL, "api-url", getenv("GITHUB_API_URL"), "GitHub API URL (default $GITHUB_API_URL, else https://api.github.com)")
	fs.StringVar(&c.summary, "summary", getenv("GITHUB_STEP_SUMMARY"), "file to append the markdown summary to (default $GITHUB_STEP_SUMMARY)")
	fs.BoolVar(&c.dryRun, "dry-run", false, "read only: show what would change")
}

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

func parse(fs *flag.FlagSet, args []string, stderr io.Writer) (int, bool) {
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "admin %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return 2, false
	}
	return 0, true
}

func fail(stderr io.Writer, cmd string, err error) int {
	_, _ = fmt.Fprintf(stderr, "admin %s: %v\n", cmd, err)
	return 1
}

func usageErr(stderr io.Writer, cmd, msg string) int {
	_, _ = fmt.Fprintf(stderr, "admin %s: %s\n", cmd, msg)
	return 2
}

func runSync(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	var c common
	c.register(fs, getenv)
	sourceDir := fs.String("source-dir", "", "local directory holding the generated .yaml manifests to propagate")
	destPath := fs.String("dest-path", "", "directory in the destination repository the files land in (e.g. config/crd/bases)")
	base := fs.String("base", "main", "destination branch to sync into")
	label := fs.String("label", "", "human label for the commit and pull request (e.g. \"CRDs from environments-api v0.3.2\")")
	autoMerge := fs.Bool("auto-merge", true, "merge the sync pull request as soon as it is opened")
	if code, ok := parse(fs, args, stderr); !ok {
		return code
	}
	if *sourceDir == "" {
		return usageErr(stderr, "sync", "missing -source-dir")
	}
	if *destPath == "" {
		return usageErr(stderr, "sync", "missing -dest-path")
	}

	files, err := readManifests(*sourceDir)
	if err != nil {
		return fail(stderr, "sync", err)
	}

	api, err := c.client(getenv)
	if err != nil {
		return fail(stderr, "sync", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	opts := admin.SyncOptions{
		DestPath:  *destPath,
		Base:      *base,
		Files:     files,
		Label:     *label,
		AutoMerge: *autoMerge,
		DryRun:    c.dryRun,
	}
	res, err := admin.Sync(ctx, api, opts)
	if err != nil {
		return fail(stderr, "sync", err)
	}
	if err := c.emit(stdout, admin.SyncSummary(res, opts)); err != nil {
		return fail(stderr, "sync", err)
	}
	return 0
}

// readManifests reads every .yaml file directly under dir, sorted by name.
func readManifests(dir string) ([]admin.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []admin.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, admin.File{Name: e.Name(), Content: string(b)})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .yaml files in %s", dir)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}
