// Package oidctest is a fake GitHub Actions OIDC issuer for tests: it serves
// a JWKS and signs tokens with its own key.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Audience is the audience Claims uses.
const Audience = "tf-plan-cost"

// Issuer is the fake issuer. Its URL is the issuer URL; the JWKS is at
// URL + "/.well-known/jwks".
type Issuer struct {
	*httptest.Server
	key *rsa.PrivateKey
}

// NewIssuer starts an issuer that is closed when the test ends.
func NewIssuer(t testing.TB) *Issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	iss := &Issuer{key: key}
	iss.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks" {
			http.NotFound(w, r)
			return
		}
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}}
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(iss.Close)
	return iss
}

// Claims returns valid claims for a pull_request run of o/r (ID 42) on
// PR 7, merge commit merge-sha, run 555.
func (iss *Issuer) Claims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": iss.URL, "aud": Audience, "sub": "repo:o@1/r@42:pull_request",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"repository": "o/r", "repository_id": "42", "repository_owner": "o",
		"ref": "refs/pull/7/merge", "sha": "merge-sha", "event_name": "pull_request",
		"run_id": "555", "run_attempt": "1",
	}
}

// Sign returns a signed JWT with the given claims.
func (iss *Issuer) Sign(t testing.TB, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: iss.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
