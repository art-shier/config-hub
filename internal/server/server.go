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

// HTTPServer is the net/http lifecycle surface used by Server. Shutdown starts
// a graceful stop, while Close force-stops the listener when graceful shutdown
// fails. Run always collects ListenAndServe before returning.
type HTTPServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
	Close() error
}

// UserReloader atomically reconciles the configured users with durable state.
type UserReloader interface {
	Reload(context.Context) error
}

type handlerDrainer interface {
	BeginDrain()
	Wait()
}

// UserReloadFunc adapts a function to UserReloader.
type UserReloadFunc func(context.Context) error

func (fn UserReloadFunc) Reload(ctx context.Context) error { return fn(ctx) }

// State is the concurrency-safe health state shared with HTTP handlers.
type State struct {
	live                         atomic.Bool
	ready                        atomic.Bool
	lastSuccessfulUserSyncAtNano atomic.Int64
}

func NewState() *State { return new(State) }

func (s *State) Live() bool  { return s != nil && s.live.Load() }
func (s *State) Ready() bool { return s != nil && s.ready.Load() }

func (s *State) LastSuccessfulUserSyncAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	nanoseconds := s.lastSuccessfulUserSyncAtNano.Load()
	if nanoseconds == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanoseconds).UTC()
}

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

func WithNow(now func() time.Time) Option {
	return func(server *Server) {
		if now != nil {
			server.now = now
		}
	}
}

func WithHandlerDrainer(drainer handlerDrainer) Option {
	return func(server *Server) { server.handlerDrainer = drainer }
}

// Server coordinates initial user synchronization, listening, reload, and
// graceful shutdown. It deliberately never logs dependency errors because a
// users file error can contain sensitive deployment material.
type Server struct {
	httpServer     HTTPServer
	reloader       UserReloader
	logger         *slog.Logger
	state          *State
	handlerDrainer handlerDrainer
	reloadSlot     chan struct{}
	lifecycleMu    sync.Mutex
	generation     uint64
	terminal       bool
	running        atomic.Bool
	now            func() time.Time
}

func New(httpServer HTTPServer, options ...Option) *Server {
	server := &Server{
		httpServer: httpServer,
		logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
		state:      NewState(),
		reloadSlot: make(chan struct{}, 1),
		now:        time.Now,
	}
	server.reloadSlot <- struct{}{}
	for _, option := range options {
		if option != nil {
			option(server)
		}
	}
	return server
}

func (s *Server) Live() bool  { return s != nil && s.state.Live() }
func (s *Server) Ready() bool { return s != nil && s.state.Ready() }
func (s *Server) LastSuccessfulUserSyncAt() time.Time {
	if s == nil || s.state == nil {
		return time.Time{}
	}
	return s.state.LastSuccessfulUserSyncAt()
}

func (s *Server) currentGeneration() uint64 {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.generation
}

func (s *Server) beginRun() uint64 {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.generation++
	s.terminal = false
	s.state.ready.Store(false)
	s.state.live.Store(true)
	return s.generation
}

func (s *Server) markTerminal(generation uint64) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if generation == s.generation {
		s.terminal = true
		s.state.ready.Store(false)
	}
}

func (s *Server) finishRun(generation uint64) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if generation == s.generation {
		s.terminal = true
		s.state.ready.Store(false)
		s.state.live.Store(false)
	}
}

func (s *Server) publishReady(generation uint64) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if generation == s.generation && !s.terminal {
		s.state.ready.Store(true)
	}
}

func (s *Server) beginHandlerDrain() {
	if s.handlerDrainer != nil {
		s.handlerDrainer.BeginDrain()
	}
}

func (s *Server) waitForHandlers() {
	if s.handlerDrainer != nil {
		s.handlerDrainer.Wait()
	}
}

// Reload reconciles users. A failure retains both the last valid database
// snapshot and its readiness state.
func (s *Server) Reload(ctx context.Context) error {
	if s == nil || s.reloader == nil {
		return ErrUserReload
	}
	if ctx == nil {
		ctx = context.Background()
	}
	generation := s.currentGeneration()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.reloadSlot:
	}
	defer func() { s.reloadSlot <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.reloader.Reload(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.Ready() {
			s.logger.Error("user reload failed", "outcome", "retaining last valid users")
		} else {
			s.logger.Error("initial user reload failed")
		}
		return ErrUserReload
	}
	s.state.lastSuccessfulUserSyncAtNano.Store(s.now().UTC().UnixNano())
	s.publishReady(generation)
	return nil
}

// Run synchronizes users before listening. Cancellation allows up to ten
// seconds for graceful shutdown, then force-closes on failure and waits for
// already-admitted handlers to exit.
func (s *Server) Run(ctx context.Context) error {
	if s == nil || s.httpServer == nil || s.reloader == nil || s.state == nil {
		return ErrInvalidServer
	}
	if !s.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	if ctx == nil {
		ctx = context.Background()
	}

	generation := s.beginRun()
	defer func() {
		s.finishRun(generation)
		s.running.Store(false)
	}()
	if err := s.Reload(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return ErrInitialUserSync
	}

	serveResult := make(chan error, 1)
	go func() { serveResult <- s.httpServer.ListenAndServe() }()

	select {
	case <-serveResult:
		s.markTerminal(generation)
		s.beginHandlerDrain()
		_ = s.httpServer.Close()
		s.waitForHandlers()
		return ErrHTTPServe
	case <-ctx.Done():
		s.markTerminal(generation)
		s.beginHandlerDrain()
		shutdownContext, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
		defer cancel()
		shutdownErr := s.httpServer.Shutdown(shutdownContext)
		if shutdownErr != nil {
			_ = s.httpServer.Close()
		}
		err := <-serveResult
		s.waitForHandlers()
		if shutdownErr != nil {
			return ErrHTTPShutdown
		}
		if !errors.Is(err, http.ErrServerClosed) {
			return ErrHTTPServe
		}
		return nil
	}
}
