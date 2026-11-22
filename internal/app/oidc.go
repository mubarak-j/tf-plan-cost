// Package app is the tf-plan-cost GitHub App server: it receives webhooks
// and plans uploaded from GitHub Actions, prices them, and reports the
// result as a Check Run and a pull request comment.
package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// GitHub Actions OIDC defaults.
const (
	ActionsIssuer   = "https://token.actions.githubusercontent.com"
	DefaultAudience = "tf-plan-cost"
)

// Claims are the GitHub Actions OIDC token claims tf-plan-cost trusts. They
// identify the repository and workflow run that uploaded a plan; nothing in
// the request body can override them.
type Claims struct {
	Repository   string `json:"repository"`    // owner/repo
	RepositoryID string `json:"repository_id"` // numeric, as a string
	Ref          string `json:"ref"`           // e.g. refs/pull/7/merge
	SHA          string `json:"sha"`           // commit the workflow ran on
	EventName    string `json:"event_name"`    // e.g. pull_request
	RunID        string `json:"run_id"`
	RunAttempt   string `json:"run_attempt"`
}

// Owner and Name split Repository.
func (c *Claims) Owner() string { o, _, _ := strings.Cut(c.Repository, "/"); return o }

// Name returns the repository name without the owner.
func (c *Claims) Name() string { _, n, _ := strings.Cut(c.Repository, "/"); return n }

// RepoID returns RepositoryID as a number.
func (c *Claims) RepoID() int64 { id, _ := strconv.ParseInt(c.RepositoryID, 10, 64); return id }

// TokenVerifier checks a GitHub Actions OIDC token and returns its claims.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (*Claims, error)
}

// OIDCVerifier verifies tokens signed by GitHub Actions' published keys.
type OIDCVerifier struct {
	v *oidc.IDTokenVerifier
}

// OIDCConfig configures an OIDCVerifier. Zero values use the GitHub
// Actions defaults.
type OIDCConfig struct {
	Issuer   string // default ActionsIssuer
	JWKSURL  string // default Issuer + "/.well-known/jwks"
	Audience string // default DefaultAudience
	Now      func() time.Time
}

// NewOIDCVerifier returns a verifier. ctx is used for fetching keys (see
// oidc.ClientContext) and must outlive the verifier.
func NewOIDCVerifier(ctx context.Context, cfg OIDCConfig) *OIDCVerifier {
	if cfg.Issuer == "" {
		cfg.Issuer = ActionsIssuer
	}
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = strings.TrimSuffix(cfg.Issuer, "/") + "/.well-known/jwks"
	}
	if cfg.Audience == "" {
		cfg.Audience = DefaultAudience
	}
	keys := oidc.NewRemoteKeySet(ctx, cfg.JWKSURL)
	return &OIDCVerifier{v: oidc.NewVerifier(cfg.Issuer, keys, &oidc.Config{
		ClientID:             cfg.Audience,
		SupportedSigningAlgs: []string{oidc.RS256},
		Now:                  cfg.Now,
	})}
}

// Verify checks the token's signature, issuer, audience and expiry, and that
// it names a repository.
func (o *OIDCVerifier) Verify(ctx context.Context, rawToken string) (*Claims, error) {
	tok, err := o.v.Verify(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	var c Claims
	if err := tok.Claims(&c); err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Claims) validate() error {
	owner, name, ok := strings.Cut(c.Repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("token has an invalid repository claim %q", c.Repository)
	}
	if id, err := strconv.ParseInt(c.RepositoryID, 10, 64); err != nil || id <= 0 {
		return fmt.Errorf("token has an invalid repository_id claim %q", c.RepositoryID)
	}
	if c.SHA == "" || c.Ref == "" {
		return errors.New("token has no sha or ref claim")
	}
	return nil
}
