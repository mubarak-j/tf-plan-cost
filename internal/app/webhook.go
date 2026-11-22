package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v93/github"

	"github.com/mubarak-j/tf-plan-cost/internal/github"
)

// handleWebhook receives GitHub App webhooks. Check Runs are created when a
// plan is uploaded, so the only events that need an answer are a user asking
// to re-run one check (check_run) or all of the App's checks (check_suite).
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
		// "Re-run" on a single tf-plan-cost check.
		var ev gh.CheckRunEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			writeError(w, http.StatusBadRequest, "invalid check_run payload")
			return
		}
		if ev.GetAction() != "rerequested" || ev.GetCheckRun().GetApp().GetID() != s.AppID {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.rerunHints(w, r, log, ev.GetRepo(), func(*gh.Client) ([]*gh.CheckRun, error) {
			return []*gh.CheckRun{ev.GetCheckRun()}, nil
		})
	case "check_suite":
		// "Re-run all checks" on the App's check suite. GitHub sends this
		// to every App with the Checks permission, without a subscription.
		var ev gh.CheckSuiteEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			writeError(w, http.StatusBadRequest, "invalid check_suite payload")
			return
		}
		if ev.GetAction() != "rerequested" || ev.GetCheckSuite().GetApp().GetID() != s.AppID {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		repo := ev.GetRepo()
		s.rerunHints(w, r, log, repo, func(client *gh.Client) ([]*gh.CheckRun, error) {
			res, _, err := client.Checks.ListCheckRunsCheckSuite(r.Context(), repo.GetOwner().GetLogin(), repo.GetName(),
				ev.GetCheckSuite().GetID(), &gh.ListCheckRunsOptions{AppID: new(s.AppID), ListOptions: gh.ListOptions{PerPage: 100}})
			if err != nil {
				return nil, fmt.Errorf("list check runs of suite %d: %w", ev.GetCheckSuite().GetID(), err)
			}
			return res.CheckRuns, nil
		})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// rerunHints adds the re-run hint to the App's check runs that runs returns.
// tf-plan-cost keeps no copy of the plan, so it cannot re-run by itself.
func (s *Server) rerunHints(w http.ResponseWriter, r *http.Request, log *slog.Logger, repo *gh.Repository,
	runs func(*gh.Client) ([]*gh.CheckRun, error)) {
	owner := repo.GetOwner().GetLogin()
	client, err := s.GitHub.RepoClient(r.Context(), owner, repo.GetName(), repo.GetID())
	if err != nil {
		log.Error("re-run: get installation client", "repo", repo.GetFullName(), "err", err)
		writeError(w, http.StatusBadGateway, "GitHub API error")
		return
	}
	list, err := runs(client)
	if err != nil {
		log.Error("re-run: list check runs", "repo", repo.GetFullName(), "err", err)
		writeError(w, http.StatusBadGateway, "GitHub API error")
		return
	}
	checks := &github.Checks{Client: client, Owner: owner, Repo: repo.GetName()}
	for _, cr := range list {
		if cr.GetApp().GetID() != 0 && cr.GetApp().GetID() != s.AppID {
			continue
		}
		if err := checks.AddRerunHint(r.Context(), cr); err != nil {
			log.Error("re-run: update check run", "repo", repo.GetFullName(), "check_run", cr.GetID(), "err", err)
			writeError(w, http.StatusBadGateway, "GitHub API error")
			return
		}
	}
	log.Info("re-run requested; added hint", "repo", repo.GetFullName(), "check_runs", len(list))
	w.WriteHeader(http.StatusNoContent)
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
