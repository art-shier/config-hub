package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"confighub.local/internal/auth"
)

const (
	SessionCookieName = "confighub_session"
	CSRFHeaderName    = "X-CSRF-Token"
)

type CredentialAuthenticator interface {
	Authenticate(context.Context, string, string) (auth.User, error)
}

type Dependencies struct {
	Credentials CredentialAuthenticator
	Sessions    *auth.SessionManager
}

type RateLimitOptions struct {
	Capacity       int
	RefillInterval time.Duration
	MaxEntries     int
}

type Options struct {
	PublicOrigin      string
	TrustedProxyCIDRs []string
	Logger            *slog.Logger
	Now               func() time.Time
	RateLimit         RateLimitOptions
	Register          func(*http.ServeMux)
}

func NewRouter(deps Dependencies, options Options) (http.Handler, error) {
	if deps.Credentials == nil || deps.Sessions == nil {
		return nil, errors.New("missing HTTP API dependencies")
	}
	origin, err := parseConfiguredOrigin(options.PublicOrigin)
	if err != nil {
		return nil, err
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	handlers := &authHandlers{credentials: deps.Credentials, sessions: deps.Sessions, publicOrigin: origin}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", handlers.login)
	mux.HandleFunc("POST /api/v1/auth/logout", handlers.logout)
	mux.HandleFunc("GET /api/v1/auth/session", handlers.session)
	mux.HandleFunc("GET /api/v1/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	if options.Register != nil {
		options.Register(mux)
	}
	allowedMethods := map[string]string{
		"/api/v1/auth/login":   http.MethodPost,
		"/api/v1/auth/logout":  http.MethodPost,
		"/api/v1/auth/session": http.MethodGet,
		"/api/v1/health/live":  http.MethodGet,
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if allowed, known := allowedMethods[r.URL.Path]; known {
			w.Header().Set("Allow", allowed)
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
			return
		}
		writeError(w, r, http.StatusNotFound, "not_found", "API route not found")
	})
	sourceIP, err := newSourceIPResolver(options.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	limiter, err := newLoginLimiter(options.RateLimit, options.Now)
	if err != nil {
		return nil, err
	}
	handlers.sourceIP = sourceIP
	handlers.limiter = limiter
	handler := securityMiddleware(mux)
	handler = accessLogMiddleware(logger, sourceIP, handler)
	handler = recoveryMiddleware(logger, handler)
	handler = requestIDMiddleware(handler)
	return handler, nil
}

func parseConfiguredOrigin(value string) (*url.URL, error) {
	origin, err := url.Parse(value)
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, errors.New("invalid public origin")
	}
	return origin, nil
}
