package app

import (
	"context"
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

// testIssuer is a fake GitHub Actions OIDC issuer: it serves a JWKS and
// signs tokens with its key.
type testIssuer struct {
	*httptest.Server
	key *rsa.PrivateKey
}

func newTestIssuer(t *testing.T) *testIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	iss := &testIssuer{key: key}
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

// Verifier returns an OIDCVerifier trusting this issuer.
func (iss *testIssuer) Verifier(ctx context.Context, now func() time.Time) *OIDCVerifier {
	return NewOIDCVerifier(ctx, OIDCConfig{Issuer: iss.URL, Now: now})
}

// Claims returns valid claims for a pull_request run of o/r (ID 42), PR 7.
func (iss *testIssuer) Claims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": iss.URL, "aud": DefaultAudience, "sub": "repo:o@1/r@42:pull_request",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"repository": "o/r", "repository_id": "42", "repository_owner": "o",
		"ref": "refs/pull/7/merge", "sha": "merge-sha", "event_name": "pull_request",
		"run_id": "555", "run_attempt": "1",
	}
}

// Sign returns a signed JWT with the given claims.
func (iss *testIssuer) Sign(t *testing.T, claims map[string]any) string {
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
