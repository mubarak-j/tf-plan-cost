// Package githubtest is an in-memory fake of the GitHub REST endpoints that
// tf-plan-cost's GitHub App uses, for tests.
package githubtest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Repo is a repository the fake knows about.
type Repo struct {
	ID int64
	// Installation is the App installation ID; 0 means not installed.
	Installation int64
	// PRs maps pull request numbers to head SHAs.
	PRs map[int]string
	// Commits maps commit SHAs to their parent SHAs.
	Commits map[string][]string
	// Files maps "sha:path" to file contents.
	Files map[string]string
}

// CheckRun is a check run created through the fake.
type CheckRun struct {
	ID         int64
	Repo       string // owner/repo
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	ExternalID string `json:"external_id"`
	DetailsURL string `json:"details_url"`
	Output     struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"output"`
}

// Comment is an issue comment.
type Comment struct {
	ID    int64
	Repo  string
	PR    int
	Body  string
	Login string
}

// Server is the fake GitHub API.
type Server struct {
	*httptest.Server
	t testing.TB

	mu      sync.Mutex
	AppSlug string
	// AppID is reported as the app of the check runs created through the
	// fake.
	AppID     int64
	Repos     map[string]*Repo // keyed by owner/repo
	CheckRuns []*CheckRun
	Comments  []*Comment
	// TokenRepoIDs records the repository_ids of each installation token
	// request.
	TokenRepoIDs [][]int64
	nextID       int64
}

// NewServer starts a fake with App slug "tf-plan-cost" and no repositories.
func NewServer(t testing.TB) *Server {
	s := &Server{t: t, AppSlug: "tf-plan-cost", Repos: map[string]*Repo{}, nextID: 1000}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// AddRepo registers owner/repo.
func (s *Server) AddRepo(fullName string, r *Repo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.PRs == nil {
		r.PRs = map[int]string{}
	}
	if r.Commits == nil {
		r.Commits = map[string][]string{}
	}
	if r.Files == nil {
		r.Files = map[string]string{}
	}
	s.Repos[fullName] = r
}

// SetPRHead moves a pull request's head, as a new push would.
func (s *Server) SetPRHead(fullName string, pr int, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Repos[fullName].PRs[pr] = sha
}

// Checks returns a copy of the check runs created so far.
func (s *Server) Checks() []CheckRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CheckRun, len(s.CheckRuns))
	for i, c := range s.CheckRuns {
		out[i] = *c
	}
	return out
}

// AllComments returns a copy of the comments so far.
func (s *Server) AllComments() []Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Comment, len(s.Comments))
	for i, c := range s.Comments {
		out[i] = *c
	}
	return out
}

// AddComment adds an existing comment.
func (s *Server) AddComment(c Comment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	c.ID = s.nextID
	s.Comments = append(s.Comments, &c)
}

func (s *Server) id() int64 {
	s.nextID++
	return s.nextID
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errorJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	auth := r.Header.Get("Authorization")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	// App (JWT) endpoints.
	if parts[0] == "app" || (len(parts) == 4 && parts[0] == "repos" && parts[3] == "installation") {
		if !strings.HasPrefix(auth, "Bearer ey") { // a JWT
			errorJSON(w, http.StatusUnauthorized, "A JSON web token could not be decoded")
			return
		}
		s.serveApp(w, r, parts)
		return
	}

	if len(parts) < 3 || parts[0] != "repos" {
		s.unexpected(w, r)
		return
	}
	fullName := parts[1] + "/" + parts[2]
	repo, ok := s.Repos[fullName]
	if !ok || !s.tokenAllows(auth, repo) {
		errorJSON(w, http.StatusNotFound, "Not Found")
		return
	}
	rest := parts[3:]
	switch {
	case r.Method == http.MethodGet && len(rest) == 0:
		writeJSON(w, http.StatusOK, map[string]any{"id": repo.ID, "full_name": fullName})

	case r.Method == http.MethodGet && len(rest) == 2 && rest[0] == "commits":
		parents, ok := repo.Commits[rest[1]]
		if !ok {
			errorJSON(w, http.StatusUnprocessableEntity, "No commit found for SHA: "+rest[1])
			return
		}
		var ps []map[string]string
		for _, p := range parents {
			ps = append(ps, map[string]string{"sha": p})
		}
		writeJSON(w, http.StatusOK, map[string]any{"sha": rest[1], "parents": ps})

	case r.Method == http.MethodGet && len(rest) == 2 && rest[0] == "pulls":
		n, _ := strconv.Atoi(rest[1])
		head, ok := repo.PRs[n]
		if !ok {
			errorJSON(w, http.StatusNotFound, "Not Found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"number": n, "state": "open", "head": map[string]string{"sha": head}})

	case r.Method == http.MethodGet && len(rest) >= 2 && rest[0] == "contents":
		path := strings.Join(rest[1:], "/")
		content, ok := repo.Files[r.URL.Query().Get("ref")+":"+path]
		if !ok {
			errorJSON(w, http.StatusNotFound, "Not Found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"type": "file", "encoding": "base64", "path": path,
			"content": base64.StdEncoding.EncodeToString([]byte(content)),
		})

	case r.Method == http.MethodPost && len(rest) == 1 && rest[0] == "check-runs":
		cr := &CheckRun{}
		if err := json.NewDecoder(r.Body).Decode(cr); err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		cr.ID, cr.Repo = s.id(), fullName
		s.CheckRuns = append(s.CheckRuns, cr)
		writeJSON(w, http.StatusCreated, s.checkJSON(cr))

	case r.Method == http.MethodGet && len(rest) == 3 && rest[0] == "check-suites" && rest[2] == "check-runs":
		// The fake has one check suite per repository: all its check runs.
		var runs []map[string]any
		for _, cr := range s.CheckRuns {
			if cr.Repo == fullName {
				runs = append(runs, s.checkJSON(cr))
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"total_count": len(runs), "check_runs": runs})

	case r.Method == http.MethodPatch && len(rest) == 2 && rest[0] == "check-runs":
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		for _, cr := range s.CheckRuns {
			if cr.ID == id && cr.Repo == fullName {
				if err := json.NewDecoder(r.Body).Decode(cr); err != nil {
					errorJSON(w, http.StatusBadRequest, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, s.checkJSON(cr))
				return
			}
		}
		errorJSON(w, http.StatusNotFound, "Not Found")

	case len(rest) == 3 && rest[0] == "issues" && rest[2] == "comments":
		n, _ := strconv.Atoi(rest[1])
		if r.Method == http.MethodGet {
			var out []map[string]any
			for _, c := range s.Comments {
				if c.Repo == fullName && c.PR == n {
					out = append(out, commentJSON(c))
				}
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		var req struct{ Body string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		c := &Comment{ID: s.id(), Repo: fullName, PR: n, Body: req.Body, Login: s.AppSlug + "[bot]"}
		s.Comments = append(s.Comments, c)
		writeJSON(w, http.StatusCreated, commentJSON(c))

	case r.Method == http.MethodPatch && len(rest) == 3 && rest[0] == "issues" && rest[1] == "comments":
		id, _ := strconv.ParseInt(rest[2], 10, 64)
		var req struct{ Body string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, c := range s.Comments {
			if c.ID == id && c.Repo == fullName {
				c.Body = req.Body
				writeJSON(w, http.StatusOK, commentJSON(c))
				return
			}
		}
		errorJSON(w, http.StatusNotFound, "Not Found")

	default:
		s.unexpected(w, r)
	}
}

func (s *Server) serveApp(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case r.Method == http.MethodGet && len(parts) == 1:
		writeJSON(w, http.StatusOK, map[string]any{"id": 1, "slug": s.AppSlug})

	case r.Method == http.MethodGet && parts[0] == "repos":
		repo, ok := s.Repos[parts[1]+"/"+parts[2]]
		if !ok || repo.Installation == 0 {
			errorJSON(w, http.StatusNotFound, "Not Found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": repo.Installation})

	case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "installations" && parts[3] == "access_tokens":
		inst, _ := strconv.ParseInt(parts[2], 10, 64)
		var req struct {
			RepositoryIDs []int64 `json:"repository_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.TokenRepoIDs = append(s.TokenRepoIDs, req.RepositoryIDs)
		for _, id := range req.RepositoryIDs {
			if !s.installed(inst, id) {
				errorJSON(w, http.StatusUnprocessableEntity, "There is at least one repository that does not exist or is not accessible to the parent installation.")
				return
			}
		}
		ids := make([]string, len(req.RepositoryIDs))
		for i, id := range req.RepositoryIDs {
			ids[i] = strconv.FormatInt(id, 10)
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"token":      fmt.Sprintf("inst-%d-repos-%s", inst, strings.Join(ids, ",")),
			"expires_at": "2099-01-01T00:00:00Z",
		})

	default:
		s.unexpected(w, r)
	}
}

func (s *Server) installed(inst, repoID int64) bool {
	for _, r := range s.Repos {
		if r.ID == repoID && r.Installation == inst {
			return true
		}
	}
	return false
}

// tokenAllows reports whether an installation token from the fake can access
// repo: it must belong to the repo's installation and, if limited to
// repository IDs, include repo's.
func (s *Server) tokenAllows(auth string, repo *Repo) bool {
	tok, ok := strings.CutPrefix(auth, "token ")
	if !ok {
		tok, ok = strings.CutPrefix(auth, "Bearer ")
	}
	if !ok {
		return false
	}
	instStr, ids, ok := strings.Cut(strings.TrimPrefix(tok, "inst-"), "-repos-")
	inst, err := strconv.ParseInt(instStr, 10, 64)
	if !ok || err != nil || inst != repo.Installation {
		return false
	}
	if ids == "" {
		return true
	}
	return slices.Contains(strings.Split(ids, ","), strconv.FormatInt(repo.ID, 10))
}

func (s *Server) checkJSON(cr *CheckRun) map[string]any {
	return map[string]any{
		"id": cr.ID, "name": cr.Name, "head_sha": cr.HeadSHA, "status": cr.Status,
		"conclusion": cr.Conclusion, "external_id": cr.ExternalID, "details_url": cr.DetailsURL,
		"html_url": fmt.Sprintf("https://github.com/%s/runs/%d", cr.Repo, cr.ID),
		"output":   map[string]string{"title": cr.Output.Title, "summary": cr.Output.Summary},
		"app":      map[string]int64{"id": s.AppID},
	}
}

func commentJSON(c *Comment) map[string]any {
	return map[string]any{
		"id": c.ID, "body": c.Body, "user": map[string]string{"login": c.Login},
		"html_url": fmt.Sprintf("https://github.com/%s/pull/%d#issuecomment-%d", c.Repo, c.PR, c.ID),
	}
}

func (s *Server) unexpected(w http.ResponseWriter, r *http.Request) {
	s.t.Errorf("githubtest: unexpected request %s %s", r.Method, r.URL)
	errorJSON(w, http.StatusNotFound, "Not Found")
}
