// Package github posts tf-plan-cost results to GitHub pull requests.
//
// It is shared by the GitHub Action (authenticated with GITHUB_TOKEN) and
// the GitHub App (authenticated with an installation token).
package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v93/github"
)

// NewClient returns a GitHub API client for token. apiURL is the REST API
// root (GITHUB_API_URL); empty means github.com. GitHub Enterprise Server
// URLs such as https://ghe.example.com/api/v3 work as is.
func NewClient(token, apiURL string) (*gh.Client, error) {
	opts := []gh.ClientOptionsFunc{gh.WithAuthToken(token), gh.WithUserAgent("tf-plan-cost")}
	if apiURL != "" && strings.TrimSuffix(apiURL, "/") != "https://api.github.com" {
		u := strings.TrimSuffix(apiURL, "/") + "/"
		opts = append(opts, gh.WithURLs(&u, &u))
	}
	return gh.NewClient(opts...)
}

// Outcome is what Upsert did.
type Outcome string

// Upsert outcomes.
const (
	Created   Outcome = "created"
	Updated   Outcome = "updated"
	Unchanged Outcome = "unchanged" // existing comment already had this body
	Skipped   Outcome = "skipped"   // no existing comment and create was false
)

// Commenter creates or updates one marked comment on a pull request.
type Commenter struct {
	Client      *gh.Client
	Owner, Repo string
	// Author, if set, limits matching to comments by this login (for example
	// github-actions[bot]), so a person quoting the marker is never edited.
	Author string
}

// UpsertResult describes the comment after Upsert.
type UpsertResult struct {
	Outcome Outcome
	URL     string
}

// Upsert finds the comment on pull request pr whose first line is marker and
// replaces its body, or creates one if none exists and create is true.
// body must start with marker.
func (c *Commenter) Upsert(ctx context.Context, pr int, marker, body string, create bool) (UpsertResult, error) {
	if firstLine(body) != marker {
		return UpsertResult{}, fmt.Errorf("comment body must start with the marker %q", marker)
	}
	existing, err := c.find(ctx, pr, marker)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("list comments on #%d: %w", pr, err)
	}
	if existing != nil {
		if existing.GetBody() == body {
			return UpsertResult{Outcome: Unchanged, URL: existing.GetHTMLURL()}, nil
		}
		updated, _, err := c.Client.Issues.UpdateComment(ctx, c.Owner, c.Repo, existing.GetID(), gh.IssueCommentRequest{Body: body})
		if err != nil {
			return UpsertResult{}, fmt.Errorf("update comment %d: %w", existing.GetID(), err)
		}
		return UpsertResult{Outcome: Updated, URL: updated.GetHTMLURL()}, nil
	}
	if !create {
		return UpsertResult{Outcome: Skipped}, nil
	}
	created, _, err := c.Client.Issues.CreateComment(ctx, c.Owner, c.Repo, pr, gh.IssueCommentRequest{Body: body})
	if err != nil {
		return UpsertResult{}, fmt.Errorf("create comment on #%d: %w", pr, err)
	}
	return UpsertResult{Outcome: Created, URL: created.GetHTMLURL()}, nil
}

// find returns the oldest matching comment, or nil.
func (c *Commenter) find(ctx context.Context, pr int, marker string) (*gh.IssueComment, error) {
	opts := &gh.IssueListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := c.Client.Issues.ListComments(ctx, c.Owner, c.Repo, pr, opts)
		if err != nil {
			return nil, err
		}
		for _, cm := range comments {
			if firstLine(cm.GetBody()) != marker {
				continue
			}
			if c.Author != "" && !strings.EqualFold(cm.GetUser().GetLogin(), c.Author) {
				continue
			}
			return cm, nil
		}
		if resp.NextPage == 0 {
			return nil, nil
		}
		opts.Page = resp.NextPage
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimRight(line, "\r")
}

// TokenLogin returns the login of the token's user. Installation tokens
// (GITHUB_TOKEN, GitHub Apps) cannot call this endpoint and return an error;
// callers then know the bot login from context instead.
func TokenLogin(ctx context.Context, client *gh.Client) (string, error) {
	u, _, err := client.Users.Get(ctx, "")
	if err != nil {
		return "", err
	}
	return u.GetLogin(), nil
}

// IsForbidden reports whether err is an HTTP 403 from the GitHub API, for
// example a read-only GITHUB_TOKEN on a pull request from a fork.
func IsForbidden(err error) bool {
	var er *gh.ErrorResponse
	return errors.As(err, &er) && er.Response != nil && er.Response.StatusCode == http.StatusForbidden
}
