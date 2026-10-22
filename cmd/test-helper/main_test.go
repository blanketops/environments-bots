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
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no command prints usage to stderr", args: nil, wantCode: 2, wantStderr: "Usage:"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "dev\n"},
		{name: "--version", args: []string{"--version"}, wantCode: 0, wantStdout: "dev\n"},
		{name: "-v", args: []string{"-v"}, wantCode: 0, wantStdout: "dev\n"},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "--help", args: []string{"--help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "-h", args: []string{"-h"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "unknown command", args: []string{"nope"}, wantCode: 2, wantStderr: `unknown command "nope"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

// checkOutput fails when want is empty but got is not, or when got does not
// contain want. A want ending in a newline must match exactly.
func checkOutput(t *testing.T, stream, got, want string) {
	t.Helper()
	switch {
	case want == "":
		if got != "" {
			t.Errorf("%s = %q, want nothing", stream, got)
		}
	case strings.HasSuffix(want, "\n"):
		if got != want {
			t.Errorf("%s = %q, want %q", stream, got, want)
		}
	case !strings.Contains(got, want):
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}

func TestVersionIsOverridable(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "v0.0.1"

	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := stdout.String(); got != "v0.0.1\n" {
		t.Errorf("stdout = %q, want %q", got, "v0.0.1\n")
	}
}
