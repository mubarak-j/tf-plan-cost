package app

import (
	"context"
	"testing"
	"time"

	"github.com/mubarak-j/tf-plan-cost/internal/app/oidctest"
)

func TestOIDCVerifier(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	ctx := context.Background()
	v := NewOIDCVerifier(ctx, OIDCConfig{Issuer: iss.URL})

	good := iss.Claims()
	claims, err := v.Verify(ctx, iss.Sign(t, good))
	if err != nil {
		t.Fatalf("Verify(valid): %v", err)
	}
	if claims.Repository != "o/r" || claims.RepoID() != 42 || claims.Owner() != "o" || claims.Name() != "r" ||
		claims.Ref != "refs/pull/7/merge" || claims.RunID != "555" {
		t.Errorf("claims = %+v", claims)
	}

	tests := map[string]func(m map[string]any){
		"wrong audience":    func(m map[string]any) { m["aud"] = "sts.amazonaws.com" },
		"wrong issuer":      func(m map[string]any) { m["iss"] = "https://evil.example.com" },
		"expired":           func(m map[string]any) { m["exp"] = time.Now().Add(-time.Minute).Unix() },
		"no repository":     func(m map[string]any) { delete(m, "repository") },
		"bad repository":    func(m map[string]any) { m["repository"] = "o/r/x" },
		"no repository_id":  func(m map[string]any) { delete(m, "repository_id") },
		"bad repository_id": func(m map[string]any) { m["repository_id"] = "abc" },
		"no sha":            func(m map[string]any) { delete(m, "sha") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := iss.Claims()
			mutate(c)
			if _, err := v.Verify(ctx, iss.Sign(t, c)); err == nil {
				t.Error("Verify succeeded")
			}
		})
	}

	t.Run("signed by another key", func(t *testing.T) {
		other := oidctest.NewIssuer(t)
		c := other.Claims()
		c["iss"] = iss.URL
		if _, err := v.Verify(ctx, other.Sign(t, c)); err == nil {
			t.Error("Verify succeeded")
		}
	})
	t.Run("garbage", func(t *testing.T) {
		if _, err := v.Verify(ctx, "not.a.jwt"); err == nil {
			t.Error("Verify succeeded")
		}
	})
}
