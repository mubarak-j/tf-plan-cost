package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v93/github"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

// DefaultMaxBodyBytes caps webhook and plan upload bodies.
const DefaultMaxBodyBytes = 5 << 20

// RepoClients returns GitHub clients authenticated as the App's installation
// on one repository. *github.App implements it.
type RepoClients interface {
	RepoClient(ctx context.Context, owner, repo string, repoID int64) (*gh.Client, error)
}

// Server handles the App's HTTP endpoints.
type Server struct {
	// AppID is the GitHub App's ID; webhooks about other Apps' check runs
	// are ignored.
	AppID int64
	// BotLogin is the App's comment author, e.g. tf-plan-cost[bot].
	BotLogin string
	// WebhookSecret is the HMAC key for X-Hub-Signature-256.
	WebhookSecret []byte
	GitHub        RepoClients
	Verifier      TokenVerifier
	// Prices is used to price uploaded plans with the App's own AWS
	// credentials.
	Prices pricing.PriceClient
	Logger *slog.Logger
	// MaxBodyBytes defaults to DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// WebURL is GitHub's web root for links to workflow runs; default
	// https://github.com.
	WebURL string
}

// Handler returns the HTTP routes:
//
//	POST /webhook   GitHub webhooks
//	POST /v1/plans  plan uploads from GitHub Actions
//	GET  /healthz   liveness
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", s.handleWebhook)
	mux.HandleFunc("POST /v1/plans", s.handlePlan)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Server) maxBody() int64 {
	if s.MaxBodyBytes > 0 {
		return s.MaxBodyBytes
	}
	return DefaultMaxBodyBytes
}

func (s *Server) webURL() string {
	if s.WebURL != "" {
		return strings.TrimSuffix(s.WebURL, "/")
	}
	return "https://github.com"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends {"error": msg}. msg is shown to the user in the Action's
// log, so it must not include secrets or internal details.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
