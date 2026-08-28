// Package server owns ConfigHub's native HTTP lifecycle.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const gracefulShutdownTimeout = 10 * time.Second

var (
	ErrInitialUserSync = errors.New("initial user synchronization failed")
	ErrUserReload      = errors.New("user synchronization failed")
	ErrHTTPServe       = errors.New("HTTP server failed")
	ErrHTTPShutdown    = errors.New("HTTP server shutdown failed")
	ErrInvalidServer   = errors.New("server dependencies are incomplete")
	ErrAlreadyRunning  = errors.New("server is already running")
)

// HTTPServer is the net/http lifecycle surface used by Server.
type HTTPServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

// UserReloader atomically reconciles the configured users with durable state.
type UserReloader interface {
	Reload(context.Context) error
}

// UserReloadFunc adapts a function to UserReloader.
type UserReloadFunc func(context.Context) error

func (fn UserReloadFunc) Reload(ctx context.Context) error { return fn(ctx) }

// State is the concurrency-safe health state shared with HTTP handlers.
type State struct {
	live  atomic.Bool
	ready atomic.Bool
}

func NewState() *State { return new(State) }

func (s *State) Live() bool  { return s != nil && s.live.Load() }
func (s *State) Ready() bool { return s != nil && s.ready.Load() }

type Option func(*Server)

func WithUserReloader(reloader UserReloader) Option {
	return func(server *Server) { server.reloader = reloader }
}

func WithLogger(logger *slog.Logger) Option {
	return func(server *Server) {
		if logger != nil {
			server.logger = logger
		}
	}
}

func WithState(state *State) Option {
	return func(server *Server) {
		if state != nil {
			server.state = state
		}
	}
}

// Server coordinates initial user synchronization, listening, reload, and
// graceful shutdown. It deliberately never logs dependency errors because a
// users file error can contain sensitive deployment material.
type Server struct {
	httpServer HTTPServer
	reloader   UserReloader
	logger     *slog.Logger
	state      *State
	reloadMu   sync.Mutex
	running    atomic.Bool
	draining   atomic.Bool
}

func New(httpServer HTTPServer, options ...Option) *Server {
	server := &Server{
		httpServer: httpServer,
		logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		state:      NewState(),
	}
	for _, option := range options {
		if option != nil {
			option(server)
		}
	}
	return server
}

func (s *Server) Live() bool  { return s != nil && s.state.Live() }
func (s *Server) Ready() bool { return s != nil && s.state.Ready() }

// Reload reconciles users. A failure retains both the last valid database
// snapshot and its readiness state.
func (s *Server) Reload(ctx context.Context) error {
	if s == nil || s.reloader == nil {
		return ErrUserReload
	}
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	if err := s.reloader.Reload(ctx); err != nil {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if s.Ready() {
			s.logger.Error("user reload failed", "outcome", "retaining last valid users")
		} else {
			s.logger.Error("initial user reload failed")
		}
		return ErrUserReload
	}
	if !s.draining.Load() {
		s.state.ready.Store(true)
	}
	return nil
}

// Run synchronizes users before listening and drains active HTTP requests for
// up to ten seconds when the context is canceled.
func (s *Server) Run(ctx context.Context) error {
	if s == nil || s.httpServer == nil || s.reloader == nil || s.state == nil {
		return ErrInvalidServer
	}
	if !s.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer s.running.Store(false)
	if ctx == nil {
		ctx = context.Background()
	}

	s.draining.Store(false)
	s.state.live.Store(true)
	defer s.state.live.Store(false)
	if err := s.Reload(ctx); err != nil {
		s.state.ready.Store(false)
		if ctx.Err() != nil {
			return nil
		}
		return ErrInitialUserSync
	}

	serveResult := make(chan error, 1)
	go func() { serveResult <- s.httpServer.ListenAndServe() }()

	select {
	case <-serveResult:
		s.state.ready.Store(false)
		return ErrHTTPServe
	case <-ctx.Done():
		s.draining.Store(true)
		s.state.ready.Store(false)
		shutdownContext, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownContext); err != nil {
			return ErrHTTPShutdown
		}
		select {
		case err := <-serveResult:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return ErrHTTPServe
			}
		default:
		}
		return nil
	}
}
