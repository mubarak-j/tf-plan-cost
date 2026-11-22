package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
	ghv88 "github.com/google/go-github/v88/github" // the version ghinstallation's options type comes from
	gh "github.com/google/go-github/v93/github"
)

// App authenticates as a GitHub App: with a JWT for App endpoints and with
// installation tokens for repository endpoints.
type App struct {
	apps   *ghinstallation.AppsTransport
	apiURL string // REST API root without a trailing slash

	mu      sync.Mutex
	clients map[installKey]*gh.Client
}

type installKey struct{ installation, repo int64 }

// ErrNotInstalled means the App is not installed on the repository.
var ErrNotInstalled = errors.New("the tf-plan-cost App is not installed on this repository")

// NewApp returns an App for appID and its PEM-encoded private key. apiURL is
// the REST API root; empty means github.com. base is the underlying
// transport; nil means http.DefaultTransport.
func NewApp(appID int64, privateKeyPEM []byte, apiURL string, base http.RoundTripper) (*App, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	atr, err := ghinstallation.NewAppsTransport(base, appID, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("GitHub App private key: %w", err)
	}
	apiURL = strings.TrimSuffix(apiURL, "/")
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	atr.BaseURL = apiURL
	return &App{apps: atr, apiURL: apiURL, clients: map[installKey]*gh.Client{}}, nil
}

func (a *App) newClient(tr http.RoundTripper) (*gh.Client, error) {
	opts := []gh.ClientOptionsFunc{gh.WithTransport(tr), gh.WithUserAgent("tf-plan-cost")}
	if a.apiURL != "https://api.github.com" {
		u := a.apiURL + "/"
		opts = append(opts, gh.WithURLs(&u, &u))
	}
	return gh.NewClient(opts...)
}

// BotLogin returns the login the App comments as, e.g. tf-plan-cost[bot].
func (a *App) BotLogin(ctx context.Context) (string, error) {
	client, err := a.newClient(a.apps)
	if err != nil {
		return "", err
	}
	app, _, err := client.Apps.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("get App: %w", err)
	}
	if app.GetSlug() == "" {
		return "", errors.New("get App: response has no slug")
	}
	return app.GetSlug() + "[bot]", nil
}

// RepoClient returns a client authenticated as the App's installation on
// owner/repo, with a token limited to that repository. repoID must match the
// repository's ID, so a repository that was deleted and re-created under the
// same name is not mistaken for the one a token was issued for.
func (a *App) RepoClient(ctx context.Context, owner, repo string, repoID int64) (*gh.Client, error) {
	jwtClient, err := a.newClient(a.apps)
	if err != nil {
		return nil, err
	}
	inst, _, err := jwtClient.Apps.GetRepositoryInstallation(ctx, owner, repo)
	if IsNotFound(err) {
		return nil, ErrNotInstalled
	}
	if err != nil {
		return nil, fmt.Errorf("find installation for %s/%s: %w", owner, repo, err)
	}

	key := installKey{inst.GetID(), repoID}
	a.mu.Lock()
	client, ok := a.clients[key]
	a.mu.Unlock()
	if !ok {
		itr := ghinstallation.NewFromAppsTransport(a.apps, inst.GetID())
		itr.InstallationTokenOptions = &ghv88.InstallationTokenOptions{RepositoryIDs: []int64{repoID}}
		if client, err = a.newClient(itr); err != nil {
			return nil, err
		}
	}

	r, _, err := client.Repositories.Get(ctx, owner, repo)
	if IsNotFound(err) || isUnprocessable(err) {
		// The installation token is limited to repoID; GitHub refuses it
		// when that ID is not in the installation.
		return nil, ErrNotInstalled
	}
	if err != nil {
		return nil, fmt.Errorf("get repository %s/%s: %w", owner, repo, err)
	}
	if r.GetID() != repoID {
		return nil, fmt.Errorf("repository %s/%s has ID %d, not %d", owner, repo, r.GetID(), repoID)
	}
	if !ok {
		a.mu.Lock()
		a.clients[key] = client
		a.mu.Unlock()
	}
	return client, nil
}

// IsNotFound reports whether err is an HTTP 404 from the GitHub API.
func IsNotFound(err error) bool { return IsStatus(err, http.StatusNotFound) }

func isUnprocessable(err error) bool {
	if IsStatus(err, http.StatusUnprocessableEntity) {
		return true
	}
	var he *ghinstallation.HTTPError
	return errors.As(err, &he) && he.Response != nil &&
		(he.Response.StatusCode == http.StatusUnprocessableEntity || he.Response.StatusCode == http.StatusNotFound)
}

// IsStatus reports whether err is a GitHub API error with HTTP status code.
func IsStatus(err error, code int) bool {
	var er *gh.ErrorResponse
	return errors.As(err, &er) && er.Response != nil && er.Response.StatusCode == code
}
