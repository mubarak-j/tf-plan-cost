package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v93/github"

	"github.com/mubarak-j/tf-plan-cost/internal/github"
)

// handleWebhook receives GitHub App webhooks. The App subscribes only to
// check_run: Check Runs are created when a plan is uploaded, and the only
// event that needs an answer is a user asking to re-run one.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBody()))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body")
		return
	}
	if !validSignature(s.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		s.logger().Warn("webhook signature check failed", "delivery", r.Header.Get("X-GitHub-Delivery"))
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	log := s.logger().With("event", event, "delivery", r.Header.Get("X-GitHub-Delivery"))
	switch event {
	case "ping":
		writeJSON(w, http.StatusOK, map[string]string{"status": "pong"})
	case "check_run":
		var ev gh.CheckRunEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			writeError(w, http.StatusBadRequest, "invalid check_run payload")
			return
		}
		if ev.GetAction() != "rerequested" || ev.GetCheckRun().GetApp().GetID() != s.AppID {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		repo := ev.GetRepo()
		client, err := s.GitHub.RepoClient(r.Context(), repo.GetOwner().GetLogin(), repo.GetName(), repo.GetID())
		if err != nil {
			log.Error("re-run: get installation client", "repo", repo.GetFullName(), "err", err)
			writeError(w, http.StatusBadGateway, "GitHub API error")
			return
		}
		checks := &github.Checks{Client: client, Owner: repo.GetOwner().GetLogin(), Repo: repo.GetName()}
		if err := checks.AddRerunHint(r.Context(), ev.GetCheckRun()); err != nil {
			log.Error("re-run: update check run", "repo", repo.GetFullName(), "err", err)
			writeError(w, http.StatusBadGateway, "GitHub API error")
			return
		}
		log.Info("re-run requested; added hint", "repo", repo.GetFullName(), "check_run", ev.GetCheckRun().GetID())
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// validSignature checks GitHub's X-Hub-Signature-256 header ("sha256=" and
// the hex HMAC-SHA256 of the body) in constant time.
func validSignature(secret, body []byte, header string) bool {
	if len(secret) == 0 {
		return false
	}
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
