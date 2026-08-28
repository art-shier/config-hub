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
	Verify(context.Context, string, string) (auth.VerifiedCredential, error)
}

type Dependencies struct {
	Credentials CredentialAuthenticator
	Sessions    *auth.SessionManager
	Projects    ProjectService
	Revisions   RevisionService
	Machines    MachineAccessService
	System      SystemStatus
}

type RateLimitOptions struct {
	Capacity         int
	SourceCapacity   int
	RefillInterval   time.Duration
	MaxEntries       int
	SourceMaxEntries int
}

type Options struct {
	PublicOrigin      string
	TrustedProxyCIDRs []string
	Logger            *slog.Logger
	Now               func() time.Time
	RateLimit         RateLimitOptions
	Register          func(*http.ServeMux)
	WebUI             http.Handler
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
	system := &systemHandlers{status: deps.System}
	mux := http.NewServeMux()
	projectsEnabled := deps.Projects != nil
	revisionsEnabled := deps.Revisions != nil
	machinesEnabled := deps.Machines != nil
	mux.HandleFunc("POST /api/v1/auth/login", handlers.login)
	mux.HandleFunc("POST /api/v1/auth/logout", handlers.logout)
	mux.HandleFunc("GET /api/v1/auth/session", handlers.session)
	mux.HandleFunc("GET /api/v1/health/live", system.live)
	mux.HandleFunc("GET /api/v1/health/ready", system.ready)
	if projectsEnabled {
		projectAPI := &projectHandlers{service: deps.Projects, auth: handlers}
		mux.HandleFunc("GET /api/v1/projects", projectAPI.list)
		mux.HandleFunc("POST /api/v1/projects", projectAPI.create)
		mux.HandleFunc("GET /api/v1/projects/{project}", projectAPI.detail)
		mux.HandleFunc("POST /api/v1/projects/{project}/environments", projectAPI.createEnvironment)
		mux.HandleFunc("GET /api/v1/projects/{project}/members", projectAPI.listMembers)
		mux.HandleFunc("PUT /api/v1/projects/{project}/members/{username}", projectAPI.setMember)
		mux.HandleFunc("DELETE /api/v1/projects/{project}/members/{username}", projectAPI.removeMember)
	}
	if revisionsEnabled {
		revisionAPI := &revisionHandlers{service: deps.Revisions, machines: deps.Machines, auth: handlers}
		mux.HandleFunc("GET /api/v1/projects/{project}/environments/{environment}/config", revisionAPI.current)
		mux.HandleFunc("PUT /api/v1/projects/{project}/environments/{environment}/config", revisionAPI.replace)
		mux.HandleFunc("GET /api/v1/projects/{project}/environments/{environment}/revisions", revisionAPI.list)
		mux.HandleFunc("GET /api/v1/projects/{project}/environments/{environment}/revisions/{version}", revisionAPI.detail)
		mux.HandleFunc("GET /api/v1/projects/{project}/environments/{environment}/revisions/{version}/diff", revisionAPI.diff)
		mux.HandleFunc("POST /api/v1/projects/{project}/environments/{environment}/revisions/{version}/rollback", revisionAPI.rollback)
	}
	if machinesEnabled {
		machineAPI := &machineHandlers{service: deps.Machines, auth: handlers}
		mux.HandleFunc("GET /api/v1/machine-identities", machineAPI.list)
		mux.HandleFunc("POST /api/v1/machine-identities", machineAPI.create)
		mux.HandleFunc("GET /api/v1/machine-identities/{identity}", machineAPI.detail)
		mux.HandleFunc("PUT /api/v1/machine-identities/{identity}", machineAPI.update)
		mux.HandleFunc("PUT /api/v1/machine-identities/{identity}/grants", machineAPI.replaceGrants)
		mux.HandleFunc("POST /api/v1/machine-identities/{identity}/tokens", machineAPI.issueToken)
		mux.HandleFunc("DELETE /api/v1/machine-identities/{identity}/tokens/{token}", machineAPI.revokeToken)
	}
	if options.Register != nil {
		options.Register(mux)
	}
	allowedMethods := map[string]string{
		"/api/v1/auth/login":   http.MethodPost,
		"/api/v1/auth/logout":  http.MethodPost,
		"/api/v1/auth/session": http.MethodGet,
		"/api/v1/health/live":  http.MethodGet,
		"/api/v1/health/ready": http.MethodGet,
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if allowed, known := allowedMethods[r.URL.Path]; known {
			w.Header().Set("Allow", allowed)
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
			return
		}
		if projectsEnabled {
			if allowed, known := projectRouteMethods(r.URL.Path); known {
				w.Header().Set("Allow", allowed)
				writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
				return
			}
		}
		if revisionsEnabled {
			if allowed, known := revisionRouteMethods(r.URL.Path); known {
				w.Header().Set("Allow", allowed)
				writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
				return
			}
		}
		if machinesEnabled {
			if allowed, known := machineRouteMethods(r.URL.Path); known {
				w.Header().Set("Allow", allowed)
				writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
				return
			}
		}
		writeError(w, r, http.StatusNotFound, "not_found", "API route not found")
	})
	if options.WebUI != nil {
		mux.Handle("/", options.WebUI)
	}
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
	var handler http.Handler = authorizationSurfaceMiddleware(machinesEnabled && revisionsEnabled, mux)
	handler = securityMiddleware(handler)
	handler = recoveryMiddleware(logger, handler)
	handler = accessLogMiddleware(logger, sourceIP, handler)
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
