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

package prreviewer

import "testing"

func TestMatchAddedReturnsNewFileLine(t *testing.T) {
	patch := "@@ -1,2 +1,4 @@\n context\n+added one\n+run: echo ${{ github.event.pull_request.title }}\n context2\n"
	// new-file lines: context=1, "added one"=2, the untrusted run=3, context2=4.
	if got := matchAdded(patch, untrustedRes); got != 3 {
		t.Fatalf("untrusted match line = %d, want 3", got)
	}
	if got := matchAdded("@@ -1 +1 @@\n+a clean added line\n", untrustedRes); got != 0 {
		t.Fatalf("clean patch matched at %d, want 0", got)
	}
}

func TestUntrustedPatternsFlagInjectionVectorsNotSafeFields(t *testing.T) {
	bad := []string{
		"run: echo ${{ github.event.pull_request.title }}",
		"        ${{ github.head_ref }}",
		"${{ github.event.issue.body }}",
	}
	for _, s := range bad {
		if matchAdded("@@ -0,0 +1 @@\n+"+s+"\n", untrustedRes) == 0 {
			t.Errorf("should flag injection vector: %q", s)
		}
	}
	// A PR number is a safe integer, not a user-controlled string.
	safe := "-pull \"${{ github.event.pull_request.number }}\""
	if matchAdded("@@ -0,0 +1 @@\n+"+safe+"\n", untrustedRes) != 0 {
		t.Errorf("PR number should not be flagged: %q", safe)
	}
}

func TestInlineCommentsKeepsOnlyLineSpecific(t *testing.T) {
	files := []FileReview{{
		Path: "a.go",
		Comments: []FileComment{
			{Line: 0, Body: "file-level note"},
			{Line: 5, Body: "line five"},
		},
	}}
	cs := inlineComments(files)
	if len(cs) != 1 || cs[0].Path != "a.go" || cs[0].Line != 5 {
		t.Fatalf("inlineComments = %+v, want one on a.go line 5", cs)
	}
}
