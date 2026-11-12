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
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blanketops/environments-bots/internal/github/githubtest"
)

func TestRun_Usage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no args", nil, 2},
		{"unknown command", []string{"bogus"}, 2},
		{"help", []string{"help"}, 0},
		{"version", []string{"version"}, 0},
		{"sync without source-dir", []string{"sync", "-dest-path", "config/crd/bases"}, 2},
		{"sync without dest-path", []string{"sync", "-source-dir", "/tmp"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			got := run(tc.args, &out, &errb, func(string) string { return "" })
			if got != tc.want {
				t.Fatalf("run(%v) = %d, want %d (stderr: %s)", tc.args, got, tc.want, errb.String())
			}
		})
	}
}

func TestRun_VersionPrints(t *testing.T) {
	var out, errb bytes.Buffer
	if got := run([]string{"version"}, &out, &errb, func(string) string { return "" }); got != 0 {
		t.Fatalf("version exit = %d", got)
	}
	if strings.TrimSpace(out.String()) != version {
		t.Fatalf("version printed %q, want %q", out.String(), version)
	}
}

func writeManifests(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRun_SyncAgainstFake(t *testing.T) {
	s := githubtest.New("blanketops", "environments-install", "tok")
	defer s.Close()
	s.Branches["main"] = "basesha0123456789"
	dir := writeManifests(t, map[string]string{
		"environments.blanketops.dev_builds.yaml": "kind: CustomResourceDefinition\n",
		"not-a-manifest.txt":                      "ignored",
	})
	getenv := func(k string) string {
		if k == "GH_TOKEN" {
			return "tok"
		}
		return ""
	}
	args := []string{
		"sync",
		"-repo", "blanketops/environments-install",
		"-api-url", s.URL,
		"-source-dir", dir,
		"-dest-path", "config/crd/bases",
		"-base", "main",
		"-label", "CRDs from environments-api v0.3.2",
	}
	var out, errb bytes.Buffer
	if got := run(args, &out, &errb, getenv); got != 0 {
		t.Fatalf("sync exit = %d (stderr: %s)", got, errb.String())
	}
	if len(s.Pulls) != 1 {
		t.Fatalf("expected one pull request, got %d", len(s.Pulls))
	}
	if !s.Pulls[0].Merged {
		t.Fatal("expected the pull request auto-merged")
	}
	if len(s.CommitsOnBranch) != 1 || len(s.CommitsOnBranch[0].Additions) != 1 {
		t.Fatalf("expected one signed commit of one file, got %+v", s.CommitsOnBranch)
	}
	if got := s.CommitsOnBranch[0].Additions[0]; got != "config/crd/bases/environments.blanketops.dev_builds.yaml" {
		t.Fatalf("committed path = %q", got)
	}
	if !strings.Contains(out.String(), "Opened and merged") {
		t.Fatalf("summary missing merge note: %s", out.String())
	}
}

func TestRun_SyncDryRun(t *testing.T) {
	s := githubtest.New("blanketops", "environments-install", "") // dry run runs without a token
	defer s.Close()
	s.Branches["main"] = "basesha0123456789"
	dir := writeManifests(t, map[string]string{"role.yaml": "rules: []\n"})
	var out, errb bytes.Buffer
	args := []string{
		"sync", "-repo", "blanketops/environments-install", "-api-url", s.URL,
		"-source-dir", dir, "-dest-path", "config/rbac", "-dry-run",
	}
	if got := run(args, &out, &errb, func(string) string { return "" }); got != 0 {
		t.Fatalf("dry-run exit = %d (stderr: %s)", got, errb.String())
	}
	if len(s.Pulls) != 0 || len(s.CommitsOnBranch) != 0 {
		t.Fatal("dry run must write nothing")
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Fatalf("summary missing dry-run note: %s", out.String())
	}
}
