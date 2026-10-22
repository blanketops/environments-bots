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

package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// This file holds the calls releaser makes: tags, commits, releases,
// milestones and pull requests, and closing issues and milestones.

// refPath escapes a ref for a URL path, keeping the slashes of branch
// names such as release/v0.1.0.
func refPath(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// list GETs every page (100 per page) of a JSON array endpoint, following
// the Link header's next page until there is none.
func list[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	var all []T
	for page := 1; ; page++ {
		q.Set("per_page", "100")
		q.Set("page", fmt.Sprint(page))
		var batch []T
		h, err := c.doHeader(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if !hasNext(h) {
			return all, nil
		}
	}
}

// CloseIssue closes an issue as completed.
func (c *Client) CloseIssue(ctx context.Context, number int) error {
	in := map[string]string{"state": "closed", "state_reason": "completed"}
	return c.do(ctx, http.MethodPatch, c.repoPath(fmt.Sprintf("/issues/%d", number)), in, nil)
}

// ListTags returns the names of every tag.
func (c *Client) ListTags(ctx context.Context) ([]string, error) {
	tags, err := list[struct{ Name string }](ctx, c, c.repoPath("/tags"), url.Values{})
	if err != nil {
		return nil, err
	}
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	return names, nil
}

// Commit is a commit's SHA and full message.
type Commit struct {
	SHA     string
	Message string
}

type apiCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

func toCommits(in []apiCommit) []Commit {
	out := make([]Commit, len(in))
	for i, c := range in {
		out[i] = Commit{SHA: c.SHA, Message: c.Commit.Message}
	}
	return out
}

// Compare returns the commits reachable from head but not from base,
// oldest first.
func (c *Client) Compare(ctx context.Context, base, head string) ([]Commit, error) {
	path := c.repoPath("/compare/" + refPath(base) + "..." + refPath(head))
	var all []Commit
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("per_page", "100")
		q.Set("page", fmt.Sprint(page))
		var out struct {
			Commits []apiCommit `json:"commits"`
		}
		h, err := c.doHeader(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &out)
		if err != nil {
			return nil, err
		}
		all = append(all, toCommits(out.Commits)...)
		if !hasNext(h) {
			return all, nil
		}
	}
}

// ListCommits returns every commit reachable from ref, newest first.
func (c *Client) ListCommits(ctx context.Context, ref string) ([]Commit, error) {
	q := url.Values{}
	q.Set("sha", ref)
	cs, err := list[apiCommit](ctx, c, c.repoPath("/commits"), q)
	return toCommits(cs), err
}

// TagSHA returns the object a tag points at; IsNotFound(err) when there is
// no such tag.
func (c *Client) TagSHA(ctx context.Context, tag string) (string, error) {
	var out struct {
		Object struct{ SHA string } `json:"object"`
	}
	err := c.do(ctx, http.MethodGet, c.repoPath("/git/ref/tags/"+refPath(tag)), nil, &out)
	return out.Object.SHA, err
}

// Release is a GitHub release.
type Release struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// ReleaseByTag returns the published release of tag; IsNotFound(err) when
// there is none (drafts are not visible by tag).
func (c *Client) ReleaseByTag(ctx context.Context, tag string) (Release, error) {
	var out Release
	err := c.do(ctx, http.MethodGet, c.repoPath("/releases/tags/"+refPath(tag)), nil, &out)
	return out, err
}

// Milestone is a repository milestone.
type Milestone struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	State      string `json:"state"`
	OpenIssues int    `json:"open_issues"`
	HTMLURL    string `json:"html_url"`
}

// ListMilestones returns every milestone, open or closed.
func (c *Client) ListMilestones(ctx context.Context) ([]Milestone, error) {
	q := url.Values{}
	q.Set("state", "all")
	return list[Milestone](ctx, c, c.repoPath("/milestones"), q)
}

// OpenMilestoneItems returns the open issues and pull requests of a
// milestone.
func (c *Client) OpenMilestoneItems(ctx context.Context, number int) ([]Issue, error) {
	q := url.Values{}
	q.Set("state", "open")
	q.Set("milestone", fmt.Sprint(number))
	return list[Issue](ctx, c, c.repoPath("/issues"), q)
}

// CloseMilestone closes a milestone.
func (c *Client) CloseMilestone(ctx context.Context, number int) error {
	return c.do(ctx, http.MethodPatch, c.repoPath(fmt.Sprintf("/milestones/%d", number)), map[string]string{"state": "closed"}, nil)
}

// Pull is a pull request.
type Pull struct {
	Number   int     `json:"number"`
	Title    string  `json:"title"`
	State    string  `json:"state"`
	HTMLURL  string  `json:"html_url"`
	MergedAt *string `json:"merged_at"`
}

// ListPulls returns the pull requests in state ("open", "closed", "all")
// into base, optionally only from branch head of this repository.
func (c *Client) ListPulls(ctx context.Context, state, base, head string) ([]Pull, error) {
	q := url.Values{}
	q.Set("state", state)
	q.Set("base", base)
	if head != "" {
		q.Set("head", c.Owner+":"+head)
	}
	return list[Pull](ctx, c, c.repoPath("/pulls"), q)
}
