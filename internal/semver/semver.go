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

// Package semver is the release-version arithmetic of the BlanketOps
// family: tags are vMAJOR.MINOR.PATCH with every part a single digit (0-9),
// counting carries (v0.0.9 is followed by v0.1.0, v0.9.9 by v1.0.0), and
// the bump a release needs comes from the Conventional Commits it contains.
package semver

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Version is a release version vMAJOR.MINOR.PATCH.
type Version struct{ Major, Minor, Patch int }

var versionRE = regexp.MustCompile(`^v([0-9])\.([0-9])\.([0-9])$`)

// Parse reads "vX.Y.Z" with every part a single digit. Larger parts
// (v0.0.10), pre-release and build suffixes are not BlanketOps release
// versions and are rejected.
func Parse(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a release version (vMAJOR.MINOR.PATCH, each part a single digit 0-9)", s)
	}
	// Each part is one ASCII digit (the pattern allows nothing else).
	return Version{Major: int(m[1][0] - '0'), Minor: int(m[2][0] - '0'), Patch: int(m[3][0] - '0')}, nil
}

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Compare returns -1, 0 or 1 as v is lower than, equal to or higher than w.
func (v Version) Compare(w Version) int {
	for _, d := range [][2]int{{v.Major, w.Major}, {v.Minor, w.Minor}, {v.Patch, w.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Bump is the size of a version step.
type Bump int

// Bumps from smallest to largest.
const (
	Patch Bump = iota
	Minor
	Major
)

func (b Bump) String() string {
	switch b {
	case Major:
		return "major"
	case Minor:
		return "minor"
	default:
		return "patch"
	}
}

// Next returns v bumped by b. Parts are single digits, so a 9 carries into
// the part above: Next(Patch) of v0.0.9 is v0.1.0, of v0.9.9 is v1.0.0.
// Past v9.x.x there is no next major: Valid reports false for the result.
func (v Version) Next(b Bump) Version {
	switch b {
	case Major:
		return Version{Major: v.Major + 1}
	case Minor:
		if v.Minor == 9 {
			return v.Next(Major)
		}
		return Version{Major: v.Major, Minor: v.Minor + 1}
	default:
		if v.Patch == 9 {
			return v.Next(Minor)
		}
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	}
}

// Valid reports whether every part of v is a single digit.
func (v Version) Valid() bool {
	for _, p := range []int{v.Major, v.Minor, v.Patch} {
		if p < 0 || p > 9 {
			return false
		}
	}
	return true
}

// StepFrom reports the smallest bump that takes prev to v (with carries,
// two bumps can give the same version: from v0.0.9 both patch and minor
// give v0.1.0, which is reported as patch). ok is false when v is not one
// of prev's successors (lower, equal, or a skipped number).
func (v Version) StepFrom(prev Version) (Bump, bool) {
	for _, b := range []Bump{Patch, Minor, Major} {
		if prev.Next(b) == v {
			return b, true
		}
	}
	return Patch, false
}

// Latest returns the highest release version among tags; tags that are
// not release versions are ignored. ok is false when there is none.
func Latest(tags []string) (Version, bool) {
	var vs []Version
	for _, t := range tags {
		if v, err := Parse(t); err == nil {
			vs = append(vs, v)
		}
	}
	if len(vs) == 0 {
		return Version{}, false
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Compare(vs[j]) < 0 })
	return vs[len(vs)-1], true
}

// Commit is one commit message, classified as a Conventional Commit.
type Commit struct {
	SHA     string
	Subject string
	// Type is the Conventional Commits type ("feat", "fix", ...); empty
	// when the subject does not follow the convention.
	Type     string
	Scope    string
	Breaking bool
	// Merge marks a merge commit, which carries no change of its own.
	Merge bool
}

var subjectRE = regexp.MustCompile(`^([a-zA-Z]+)(?:\(([^()]*)\))?(!)?: \S`)

// ParseCommit classifies the full commit message msg.
func ParseCommit(sha, msg string) Commit {
	subject, body, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	subject = strings.TrimSpace(subject)
	c := Commit{SHA: sha, Subject: subject}
	if strings.HasPrefix(subject, "Merge pull request ") || strings.HasPrefix(subject, "Merge branch ") ||
		strings.HasPrefix(subject, "Merge remote-tracking branch ") {
		c.Merge = true
		return c
	}
	if m := subjectRE.FindStringSubmatch(subject); m != nil {
		c.Type = strings.ToLower(m[1])
		c.Scope = m[2]
		c.Breaking = m[3] == "!"
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "BREAKING CHANGE:") || strings.HasPrefix(line, "BREAKING-CHANGE:") {
			c.Breaking = true
		}
	}
	return c
}

// Conventional reports whether the subject follows Conventional Commits.
// Merge commits count as conventional: they add no change of their own.
func (c Commit) Conventional() bool { return c.Merge || c.Type != "" }

// Required returns the smallest bump from prev that the commits allow.
// From 1.0.0 on: a breaking change needs major, a feat needs minor,
// anything else patch. Before 1.0.0 the public API is not yet stable, so
// a breaking change needs minor and everything else patch.
func Required(prev Version, commits []Commit) Bump {
	b := Patch
	for _, c := range commits {
		if c.Merge {
			continue
		}
		need := Patch
		switch {
		case c.Breaking && prev.Major >= 1:
			need = Major
		case c.Breaking, c.Type == "feat" && prev.Major >= 1:
			need = Minor
		}
		if need > b {
			b = need
		}
	}
	return b
}
