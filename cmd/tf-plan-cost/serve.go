package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mubarak-j/tf-plan-cost/internal/app"
	"github.com/mubarak-j/tf-plan-cost/internal/github"
)

// serveConfig is the App server's configuration, from the environment.
type serveConfig struct {
	appID         int64
	privateKey    []byte
	webhookSecret []byte
	addr          string
	apiURL        string
	webURL        string
	audience      string
	issuer        string
	cacheDir      string
}

const serveUsage = `Usage: tf-plan-cost serve

Runs the tf-plan-cost GitHub App server: POST /webhook, POST /v1/plans and
GET /healthz. Configured with environment variables (see .env.example):

  GITHUB_APP_ID               App ID (required)
  GITHUB_PRIVATE_KEY          App private key: PEM contents or a path to the PEM file (required)
  GITHUB_WEBHOOK_SECRET       webhook secret (required)
  PORT                        listen port (default 8080)
  TF_PLAN_COST_LISTEN_HOST    listen address (default: all interfaces)
  GITHUB_API_URL              REST API root (default https://api.github.com)
  GITHUB_SERVER_URL           web root for links (default https://github.com)
  TF_PLAN_COST_OIDC_AUDIENCE  expected token audience (default tf-plan-cost)
  TF_PLAN_COST_CACHE_DIR      price cache directory (default: memory only)

Prices are read with the server's own AWS credentials (pricing:GetProducts).
`

func loadServeConfig(getenv func(string) string) (*serveConfig, error) {
	var missing []string
	need := func(name string) string {
		v := getenv(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}
	idStr, key, secret := need("GITHUB_APP_ID"), need("GITHUB_PRIVATE_KEY"), need("GITHUB_WEBHOOK_SECRET")
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing environment variables: %s", strings.Join(missing, ", "))
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("GITHUB_APP_ID %q is not a number", idStr)
	}
	pemKey := []byte(key)
	if !strings.Contains(key, "-----BEGIN") {
		if pemKey, err = os.ReadFile(key); err != nil {
			return nil, fmt.Errorf("GITHUB_PRIVATE_KEY is neither a PEM key nor a readable file: %w", err)
		}
	}
	port := getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return &serveConfig{
		appID:         id,
		privateKey:    pemKey,
		webhookSecret: []byte(secret),
		addr:          net.JoinHostPort(getenv("TF_PLAN_COST_LISTEN_HOST"), port),
		apiURL:        getenv("GITHUB_API_URL"),
		webURL:        getenv("GITHUB_SERVER_URL"),
		audience:      getenv("TF_PLAN_COST_OIDC_AUDIENCE"),
		issuer:        getenv("TF_PLAN_COST_OIDC_ISSUER"), // tests only
		cacheDir:      getenv("TF_PLAN_COST_CACHE_DIR"),
	}, nil
}

// runServe runs the App server until ctx is cancelled. onListen, if set, is
// called with the listening address (for tests).
func runServe(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer,
	newClient clientFactory, onListen func(addr string)) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, serveUsage) }
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	log := slog.New(slog.NewJSONHandler(stderr, nil))
	if err := serve(ctx, getenv, log, newClient, onListen); err != nil {
		log.Error("server failed", "err", err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, getenv func(string) string, log *slog.Logger,
	newClient clientFactory, onListen func(addr string)) error {
	cfg, err := loadServeConfig(getenv)
	if err != nil {
		return err
	}
	ghApp, err := github.NewApp(cfg.appID, cfg.privateKey, cfg.apiURL, nil)
	if err != nil {
		return err
	}
	bot, err := ghApp.BotLogin(ctx)
	if err != nil {
		return fmt.Errorf("check App credentials: %w", err)
	}
	// An empty cache dir keeps prices in memory only (Lambda has no
	// persistent disk anyway).
	prices, err := newClient(ctx, cfg.cacheDir, false)
	if err != nil {
		return err
	}

	srv := &app.Server{
		AppID:         cfg.appID,
		BotLogin:      bot,
		WebhookSecret: cfg.webhookSecret,
		GitHub:        ghApp,
		Verifier:      app.NewOIDCVerifier(context.WithoutCancel(ctx), app.OIDCConfig{Issuer: cfg.issuer, Audience: cfg.audience}),
		Prices:        prices,
		Logger:        log,
		WebURL:        cfg.webURL,
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute, // pricing a large plan makes many API calls
		IdleTimeout:       2 * time.Minute,
	}
	ln, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return err
	}
	log.Info("listening", "addr", ln.Addr().String(), "app", bot)
	if onListen != nil {
		onListen(ln.Addr().String())
	}

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
