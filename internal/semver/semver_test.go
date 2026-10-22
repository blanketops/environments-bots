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

package semver

import "testing"

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParse(t *testing.T) {
	for _, bad := range []string{"", "0.1.0", "v1.2", "v1.2.3-rc.1", "v01.2.3", "v1.2.3+b", "vx.1.2", "v99999999999999999999.0.0", "v0.99999999999999999999.0", "v0.0.99999999999999999999"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
	if v := mustParse(t, "v10.0.7"); v != (Version{10, 0, 7}) || v.String() != "v10.0.7" {
		t.Errorf("Parse(v10.0.7) = %+v", v)
	}
}

func TestCompareAndLatest(t *testing.T) {
	a, b := mustParse(t, "v0.8.10"), mustParse(t, "v0.8.9")
	if a.Compare(b) != 1 || b.Compare(a) != -1 || a.Compare(a) != 0 {
		t.Error("v0.8.10 must sort above v0.8.9 numerically")
	}
	if mustParse(t, "v1.0.0").Compare(mustParse(t, "v0.9.9")) != 1 || mustParse(t, "v0.2.0").Compare(mustParse(t, "v0.1.9")) != 1 {
		t.Error("major and minor must dominate")
	}
	got, ok := Latest([]string{"v0.8.9", "latest", "v0.8.10", "v0.9.0-rc.1", "v0.2.0"})
	if !ok || got.String() != "v0.8.10" {
		t.Errorf("Latest = %v, %v; want v0.8.10", got, ok)
	}
	if _, ok := Latest([]string{"nightly"}); ok {
		t.Error("Latest with no release tag must report none")
	}
}

func TestStepFrom(t *testing.T) {
	prev := mustParse(t, "v0.8.7")
	for s, want := range map[string]Bump{"v0.8.8": Patch, "v0.9.0": Minor, "v1.0.0": Major} {
		if b, ok := mustParse(t, s).StepFrom(prev); !ok || b != want {
			t.Errorf("%s from v0.8.7 = %v, %v; want %v", s, b, ok, want)
		}
	}
	for _, s := range []string{"v0.8.9", "v0.8.7", "v0.0.3", "v0.9.1", "v2.0.0"} {
		if _, ok := mustParse(t, s).StepFrom(prev); ok {
			t.Errorf("%s from v0.8.7 accepted; it is not a next version", s)
		}
	}
	if Major.String() != "major" || Minor.String() != "minor" || Patch.String() != "patch" {
		t.Error("Bump names")
	}
}

func TestParseCommit(t *testing.T) {
	cases := []struct {
		msg                   string
		typ, scope            string
		breaking, merge, conv bool
	}{
		{"feat(test-helper): add the entry point", "feat", "test-helper", false, false, true},
		{"fix!: drop the old flag", "fix", "", true, false, true},
		{"refactor(api)!: rename", "refactor", "api", true, false, true},
		{"chore: tidy\n\nBREAKING CHANGE: config moved", "chore", "", true, false, true},
		{"docs: x\n\nBREAKING-CHANGE: y", "docs", "", true, false, true},
		{"Merge pull request #2 from blanketops/feat/x\n\nfeat!: nope", "", "", false, true, true},
		{"Merge branch 'main' into develop", "", "", false, true, true},
		{"Update README.md", "", "", false, false, false},
		{"feat:missing space", "", "", false, false, false},
	}
	for _, c := range cases {
		got := ParseCommit("abc", c.msg)
		if got.Type != c.typ || got.Scope != c.scope || got.Breaking != c.breaking || got.Merge != c.merge || got.Conventional() != c.conv {
			t.Errorf("ParseCommit(%q) = %+v", c.msg, got)
		}
	}
}

func TestRequired(t *testing.T) {
	commits := func(msgs ...string) []Commit {
		var cs []Commit
		for _, m := range msgs {
			cs = append(cs, ParseCommit("", m))
		}
		return cs
	}
	pre, stable := mustParse(t, "v0.8.7"), mustParse(t, "v1.2.3")
	cases := []struct {
		prev Version
		msgs []string
		want Bump
	}{
		{pre, []string{"fix: a", "chore: b"}, Patch},
		{pre, []string{"feat: a"}, Patch},
		{pre, []string{"feat!: a"}, Minor},
		{pre, []string{"Merge pull request #1\n\nBREAKING CHANGE: x"}, Patch},
		{stable, []string{"fix: a"}, Patch},
		{stable, []string{"fix: a", "feat: b"}, Minor},
		{stable, []string{"feat: b", "fix!: c"}, Major},
		{stable, nil, Patch},
	}
	for _, c := range cases {
		if got := Required(c.prev, commits(c.msgs...)); got != c.want {
			t.Errorf("Required(%v, %q) = %v, want %v", c.prev, c.msgs, got, c.want)
		}
	}
}
