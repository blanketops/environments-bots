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

// Package github is the small part of the GitHub REST and GraphQL APIs that
// test-helper uses: issues, labels, comments and Projects (v2) items. It
// authenticates with the token the calling workflow passes in, normally an
// installation token of the BlanketOps-Environments App.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the public GitHub API.
const DefaultBaseURL = "https://api.github.com"

// Client calls the API for one repository.
type Client struct {
	BaseURL string
	Token   string
	Owner   string
	Repo    string
	HTTP    *http.Client
}

// New returns a client for the repository "owner/name".
func New(baseURL, token, repository string) (*Client, error) {
	owner, repo, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return nil, fmt.Errorf("repository %q is not owner/name", repository)
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		Token:   token,
		Owner:   owner,
		Repo:    repo,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// APIError is a non-2xx response.
type APIError struct {
	Method, Path string
	Status       int
	Message      string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
}

// IsNotFound reports whether err is an HTTP 404.
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	_, err := c.doHeader(ctx, method, path, in, out)
	return err
}

// doHeader is do that also returns the response headers.
func (c *Client) doHeader(ctx context.Context, method, path string, in, out any) (http.Header, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var msg struct{ Message string }
		_ = json.Unmarshal(data, &msg)
		if msg.Message == "" {
			msg.Message = strings.TrimSpace(string(data))
		}
		return nil, &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: msg.Message}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("%s %s: decode response: %w", method, path, err)
		}
	}
	return resp.Header, nil
}

// hasNext reports whether a Link header announces another page.
func hasNext(h http.Header) bool {
	for _, link := range h.Values("Link") {
		for _, part := range strings.Split(link, ",") {
			if strings.Contains(part, `rel="next"`) {
				return true
			}
		}
	}
	return false
}

func (c *Client) repoPath(suffix string) string {
	return "/repos/" + url.PathEscape(c.Owner) + "/" + url.PathEscape(c.Repo) + suffix
}

// Issue is a repository issue.
type Issue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	NodeID  string `json:"node_id"`
	HTMLURL string `json:"html_url"`
	// PullRequest is set when the "issue" is a pull request; the issues
	// API lists both.
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

// ListIssues returns every issue, open or closed, carrying label. It
// follows the Link header's next page until there is none.
func (c *Client) ListIssues(ctx context.Context, label string) ([]Issue, error) {
	const perPage = 100
	var all []Issue
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("state", "all")
		q.Set("labels", label)
		q.Set("per_page", fmt.Sprint(perPage))
		q.Set("page", fmt.Sprint(page))
		var batch []Issue
		h, err := c.doHeader(ctx, http.MethodGet, c.repoPath("/issues?"+q.Encode()), nil, &batch)
		if err != nil {
			return nil, err
		}
		for _, is := range batch {
			if is.PullRequest == nil {
				all = append(all, is)
			}
		}
		if !hasNext(h) {
			return all, nil
		}
	}
}

// CreateIssue opens an issue.
func (c *Client) CreateIssue(ctx context.Context, title, body string, labels []string) (Issue, error) {
	in := map[string]any{"title": title, "body": body, "labels": labels}
	var out Issue
	err := c.do(ctx, http.MethodPost, c.repoPath("/issues"), in, &out)
	return out, err
}

// Comment adds a comment to an issue.
func (c *Client) Comment(ctx context.Context, number int, body string) error {
	return c.do(ctx, http.MethodPost, c.repoPath(fmt.Sprintf("/issues/%d/comments", number)), map[string]string{"body": body}, nil)
}

// EnsureLabel creates the label unless it exists.
func (c *Client) EnsureLabel(ctx context.Context, name, color, description string) error {
	err := c.do(ctx, http.MethodGet, c.repoPath("/labels/"+url.PathEscape(name)), nil, nil)
	if err == nil || !IsNotFound(err) {
		return err
	}
	in := map[string]string{"name": name, "color": color, "description": description}
	return c.do(ctx, http.MethodPost, c.repoPath("/labels"), in, nil)
}

// graphql runs a GraphQL query; errors in the response are returned.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	var resp struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := c.do(ctx, http.MethodPost, "/graphql", map[string]any{"query": query, "variables": vars}, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, len(resp.Errors))
		for i, e := range resp.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("graphql: %s", strings.Join(msgs, "; "))
	}
	return json.Unmarshal(resp.Data, out)
}

// Project is an organization's Projects (v2) board and its single-select
// Status field.
type Project struct {
	ID            string
	StatusFieldID string
	// StatusOptions maps an option name (e.g. "Test") to its ID.
	StatusOptions map[string]string
}

// FindProject looks up project number of the organization org.
func (c *Client) FindProject(ctx context.Context, org string, number int) (Project, error) {
	const q = `query($org: String!, $number: Int!) {
  organization(login: $org) {
    projectV2(number: $number) {
      id
      field(name: "Status") {
        ... on ProjectV2SingleSelectField { id options { id name } }
      }
    }
  }
}`
	var out struct {
		Organization struct {
			ProjectV2 *struct {
				ID    string
				Field *struct {
					ID      string
					Options []struct{ ID, Name string }
				}
			}
		}
	}
	if err := c.graphql(ctx, q, map[string]any{"org": org, "number": number}, &out); err != nil {
		return Project{}, fmt.Errorf("find project %s/%d: %w", org, number, err)
	}
	p := out.Organization.ProjectV2
	if p == nil {
		return Project{}, fmt.Errorf("find project %s/%d: not found", org, number)
	}
	proj := Project{ID: p.ID, StatusOptions: map[string]string{}}
	if p.Field != nil {
		proj.StatusFieldID = p.Field.ID
		for _, o := range p.Field.Options {
			proj.StatusOptions[o.Name] = o.ID
		}
	}
	return proj, nil
}

// AddToProject adds the issue (by node ID) to the project and sets its
// Status to status. Adding an issue that is already on the board returns
// its existing item, so this is safe to repeat.
func (c *Client) AddToProject(ctx context.Context, p Project, issueNodeID, status string) error {
	optionID, ok := p.StatusOptions[status]
	if !ok || p.StatusFieldID == "" {
		return fmt.Errorf("project has no Status option %q", status)
	}
	const add = `mutation($project: ID!, $content: ID!) {
  addProjectV2ItemById(input: {projectId: $project, contentId: $content}) { item { id } }
}`
	var added struct {
		AddProjectV2ItemByID struct {
			Item struct{ ID string }
		} `json:"addProjectV2ItemById"`
	}
	if err := c.graphql(ctx, add, map[string]any{"project": p.ID, "content": issueNodeID}, &added); err != nil {
		return fmt.Errorf("add to project: %w", err)
	}
	const set = `mutation($project: ID!, $item: ID!, $field: ID!, $option: String!) {
  updateProjectV2ItemFieldValue(input: {projectId: $project, itemId: $item, fieldId: $field, value: {singleSelectOptionId: $option}}) { projectV2Item { id } }
}`
	var updated json.RawMessage
	vars := map[string]any{"project": p.ID, "item": added.AddProjectV2ItemByID.Item.ID, "field": p.StatusFieldID, "option": optionID}
	if err := c.graphql(ctx, set, vars, &updated); err != nil {
		return fmt.Errorf("set project status: %w", err)
	}
	return nil
}
