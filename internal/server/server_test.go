package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestRunPublishesLifecycleAndDrainsForTenSeconds(t *testing.T) {
	httpServer := newFakeHTTPServer()
	reloader := &fakeReloader{}
	server := New(httpServer, WithUserReloader(reloader), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	select {
	case <-httpServer.started:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not start listening")
	}
	if !server.Live() || !server.Ready() {
		t.Fatalf("running state live=%v ready=%v", server.Live(), server.Ready())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	if server.Live() || server.Ready() {
		t.Fatalf("stopped state live=%v ready=%v", server.Live(), server.Ready())
	}
	calls, timeout := httpServer.shutdownState()
	if calls != 1 || timeout < 9*time.Second || timeout > 10*time.Second {
		t.Fatalf("shutdown calls=%d timeout=%s", calls, timeout)
	}
}

func TestRunRefusesToListenWhenInitialUserSyncFails(t *testing.T) {
	httpServer := newFakeHTTPServer()
	secret := "DO_NOT_LOG_users_file_password_or_path"
	reloader := &fakeReloader{results: []error{errors.New(secret)}}
	logs := new(bytes.Buffer)
	server := New(httpServer, WithUserReloader(reloader), WithLogger(slog.New(slog.NewTextHandler(logs, nil))))

	err := server.Run(context.Background())
	if !errors.Is(err, ErrInitialUserSync) {
		t.Fatalf("error=%v, want ErrInitialUserSync", err)
	}
	select {
	case <-httpServer.started:
		t.Fatal("listened before initial user synchronization succeeded")
	default:
	}
	if server.Live() || server.Ready() {
		t.Fatalf("failed state live=%v ready=%v", server.Live(), server.Ready())
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("reload failure leaked sensitive error: %s", logs.String())
	}
}

func TestRunTreatsUnexpectedListenerExitAsRuntimeError(t *testing.T) {
	for _, test := range []struct {
		name     string
		serveErr error
	}{
		{name: "server closed", serveErr: http.ErrServerClosed},
		{name: "nil", serveErr: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := New(immediateHTTPServer{err: test.serveErr},
				WithUserReloader(&fakeReloader{}),
				WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
			)

			if err := server.Run(context.Background()); !errors.Is(err, ErrHTTPServe) {
				t.Fatalf("error=%v, want ErrHTTPServe", err)
			}
			if server.Live() || server.Ready() {
				t.Fatalf("failed state live=%v ready=%v", server.Live(), server.Ready())
			}
		})
	}
}

func TestListenerExitPreventsInFlightReloadFromRestoringReadiness(t *testing.T) {
	httpServer := newControlledHTTPServer(errors.New("listener failed"))
	reloader := &terminalRaceReloader{reloadStarted: make(chan struct{}), releaseReload: make(chan struct{})}
	server := New(httpServer,
		WithUserReloader(reloader),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(context.Background()) }()
	<-httpServer.started

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- server.Reload(context.Background()) }()
	<-reloader.reloadStarted
	close(httpServer.releaseServe)
	if err := <-runDone; !errors.Is(err, ErrHTTPServe) {
		t.Fatalf("Run error=%v, want ErrHTTPServe", err)
	}
	if server.Ready() {
		t.Fatal("listener exit left server ready")
	}

	close(reloader.releaseReload)
	if err := <-reloadDone; err != nil {
		t.Fatal(err)
	}
	if server.Ready() {
		t.Fatal("in-flight reload restored readiness after listener exit")
	}
}

func TestShutdownWaitsForServeAndReportsConcurrentListenerError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		httpServer := newControlledHTTPServer(errors.New("accept failed during shutdown"))
		server := New(httpServer,
			WithUserReloader(&fakeReloader{}),
			WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		)
		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan error, 1)
		go func() { runDone <- server.Run(ctx) }()
		<-httpServer.started
		cancel()
		<-httpServer.serveStopping
		synctest.Wait()

		select {
		case err := <-runDone:
			close(httpServer.releaseServe)
			synctest.Wait()
			t.Fatalf("Run returned before ListenAndServe completed: %v", err)
		default:
		}
		close(httpServer.releaseServe)
		if err := <-runDone; !errors.Is(err, ErrHTTPServe) {
			t.Fatalf("Run error=%v, want ErrHTTPServe", err)
		}
	})
}

func TestShutdownClassifiesListenerResult(t *testing.T) {
	for _, test := range []struct {
		name     string
		serveErr error
		wantErr  error
	}{
		{name: "server closed", serveErr: http.ErrServerClosed},
		{name: "wrapped server closed", serveErr: fmt.Errorf("serve stopped: %w", http.ErrServerClosed)},
		{name: "nil", serveErr: nil, wantErr: ErrHTTPServe},
		{name: "unexpected", serveErr: errors.New("accept failed"), wantErr: ErrHTTPServe},
	} {
		t.Run(test.name, func(t *testing.T) {
			httpServer := newShutdownResultHTTPServer(test.serveErr)
			server := New(httpServer,
				WithUserReloader(&fakeReloader{}),
				WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
			)
			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- server.Run(ctx) }()
			<-httpServer.started
			cancel()
			gotErr := <-runDone
			if test.wantErr == nil {
				if gotErr != nil {
					t.Fatalf("Run error=%v, want nil", gotErr)
				}
			} else if !errors.Is(gotErr, test.wantErr) {
				t.Fatalf("Run error=%v, want %v", gotErr, test.wantErr)
			}
		})
	}
}

func TestCancellationPreventsInFlightReloadFromRestoringReadinessBeforeShutdownReturns(t *testing.T) {
	httpServer := newBlockingShutdownHTTPServer()
	reloader := &terminalRaceReloader{reloadStarted: make(chan struct{}), releaseReload: make(chan struct{})}
	server := New(httpServer,
		WithUserReloader(reloader),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(ctx) }()
	<-httpServer.started

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- server.Reload(context.Background()) }()
	<-reloader.reloadStarted
	cancel()
	<-httpServer.shutdownStarted
	close(reloader.releaseReload)
	if err := <-reloadDone; err != nil {
		t.Fatal(err)
	}
	readyDuringShutdown := server.Ready()
	close(httpServer.releaseShutdown)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if readyDuringShutdown {
		t.Fatal("in-flight reload restored readiness while Shutdown was blocked")
	}
}

func TestStaleReloadCannotPublishReadinessForLaterRun(t *testing.T) {
	reloader := &blockingReloader{started: make(chan struct{}), release: make(chan struct{})}
	server := New(nil,
		WithUserReloader(reloader),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	firstGeneration := server.beginRun()
	reloadDone := make(chan error, 1)
	go func() { reloadDone <- server.Reload(context.Background()) }()
	<-reloader.started

	server.finishRun(firstGeneration)
	secondGeneration := server.beginRun()
	close(reloader.release)
	if err := <-reloadDone; err != nil {
		t.Fatal(err)
	}
	readyDuringSecondRun := server.Ready()
	server.finishRun(secondGeneration)
	if readyDuringSecondRun {
		t.Fatal("stale reload published readiness for a later run")
	}
}

func TestRunTreatsCancellationDuringInitialSyncAsGracefulStop(t *testing.T) {
	httpServer := newFakeHTTPServer()
	reloader := &cancelAwareReloader{started: make(chan struct{})}
	logs := new(bytes.Buffer)
	server := New(httpServer, WithUserReloader(reloader), WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	<-reloader.started
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("canceled startup error=%v", err)
	}
	select {
	case <-httpServer.started:
		t.Fatal("canceled startup reached the listener")
	default:
	}
	if logs.Len() != 0 {
		t.Fatalf("graceful startup cancellation logged an error: %s", logs.String())
	}
}

func TestReloadKeepsLastValidUsers(t *testing.T) {
	secret := "invalid users file: password=secret path=/private/users.yaml"
	reloader := &fakeReloader{results: []error{nil, errors.New(secret)}}
	logs := new(bytes.Buffer)
	server := New(newFakeHTTPServer(), WithUserReloader(reloader), WithLogger(slog.New(slog.NewTextHandler(logs, nil))))

	if err := server.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := server.Reload(context.Background()); !errors.Is(err, ErrUserReload) {
		t.Fatalf("error=%v, want ErrUserReload", err)
	}
	if calls := reloader.callCount(); calls != 2 || !server.Ready() {
		t.Fatalf("calls=%d ready=%v", calls, server.Ready())
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "password=") || strings.Contains(logs.String(), "/private/") {
		t.Fatalf("reload log leaked sensitive details: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "retaining last valid users") {
		t.Fatalf("reload log omitted safe outcome: %s", logs.String())
	}
}

func TestReloadSerializesInitialAndSignalReconciliation(t *testing.T) {
	reloader := &serializedReloader{
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		releaseFirst:  make(chan struct{}),
	}
	server := New(newFakeHTTPServer(), WithUserReloader(reloader), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- server.Reload(context.Background()) }()
	<-reloader.firstStarted
	go func() { secondDone <- server.Reload(context.Background()) }()

	overlapped := false
	select {
	case <-reloader.secondStarted:
		overlapped = true
	case <-time.After(50 * time.Millisecond):
	}
	close(reloader.releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if overlapped {
		t.Fatal("second reload entered reconciliation before the initial reload completed")
	}
}

type fakeReloader struct {
	mu      sync.Mutex
	results []error
	calls   int
}

type terminalRaceReloader struct {
	mu            sync.Mutex
	calls         int
	reloadStarted chan struct{}
	releaseReload chan struct{}
}

type blockingReloader struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingReloader) Reload(context.Context) error {
	close(r.started)
	<-r.release
	return nil
}

func (r *terminalRaceReloader) Reload(context.Context) error {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 2 {
		close(r.reloadStarted)
		<-r.releaseReload
	}
	return nil
}

type serializedReloader struct {
	mu            sync.Mutex
	calls         int
	firstStarted  chan struct{}
	secondStarted chan struct{}
	releaseFirst  chan struct{}
}

type cancelAwareReloader struct{ started chan struct{} }

func (r *cancelAwareReloader) Reload(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	return ctx.Err()
}

func (r *serializedReloader) Reload(context.Context) error {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 {
		close(r.firstStarted)
		<-r.releaseFirst
	}
	if call == 2 {
		close(r.secondStarted)
	}
	return nil
}

func (r *fakeReloader) Reload(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.calls
	r.calls++
	if index < len(r.results) {
		return r.results[index]
	}
	return nil
}

func (r *fakeReloader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type fakeHTTPServer struct {
	started chan struct{}
	stopped chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   int
	timeout time.Duration
}

type immediateHTTPServer struct{ err error }

type controlledHTTPServer struct {
	started        chan struct{}
	releaseServe   chan struct{}
	shutdownCalled chan struct{}
	serveStopping  chan struct{}
	shutdownOnce   sync.Once
	serveErr       error
}

type blockingShutdownHTTPServer struct {
	started         chan struct{}
	stopped         chan struct{}
	shutdownStarted chan struct{}
	releaseShutdown chan struct{}
}

type shutdownResultHTTPServer struct {
	started  chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	serveErr error
}

func (s immediateHTTPServer) ListenAndServe() error        { return s.err }
func (immediateHTTPServer) Shutdown(context.Context) error { return nil }

func newControlledHTTPServer(serveErr error) *controlledHTTPServer {
	return &controlledHTTPServer{
		started: make(chan struct{}), releaseServe: make(chan struct{}),
		shutdownCalled: make(chan struct{}), serveStopping: make(chan struct{}), serveErr: serveErr,
	}
}

func (s *controlledHTTPServer) ListenAndServe() error {
	close(s.started)
	select {
	case <-s.releaseServe:
	case <-s.shutdownCalled:
		close(s.serveStopping)
		<-s.releaseServe
	}
	return s.serveErr
}

func (s *controlledHTTPServer) Shutdown(context.Context) error {
	s.shutdownOnce.Do(func() { close(s.shutdownCalled) })
	return nil
}

func newBlockingShutdownHTTPServer() *blockingShutdownHTTPServer {
	return &blockingShutdownHTTPServer{
		started:         make(chan struct{}),
		stopped:         make(chan struct{}),
		shutdownStarted: make(chan struct{}),
		releaseShutdown: make(chan struct{}),
	}
}

func (s *blockingShutdownHTTPServer) ListenAndServe() error {
	close(s.started)
	<-s.stopped
	return http.ErrServerClosed
}

func (s *blockingShutdownHTTPServer) Shutdown(context.Context) error {
	close(s.shutdownStarted)
	<-s.releaseShutdown
	close(s.stopped)
	return nil
}

func newShutdownResultHTTPServer(serveErr error) *shutdownResultHTTPServer {
	return &shutdownResultHTTPServer{
		started:  make(chan struct{}),
		stop:     make(chan struct{}),
		serveErr: serveErr,
	}
}

func (s *shutdownResultHTTPServer) ListenAndServe() error {
	close(s.started)
	<-s.stop
	return s.serveErr
}

func (s *shutdownResultHTTPServer) Shutdown(context.Context) error {
	s.stopOnce.Do(func() { close(s.stop) })
	return nil
}

func newFakeHTTPServer() *fakeHTTPServer {
	return &fakeHTTPServer{started: make(chan struct{}), stopped: make(chan struct{})}
}

func (s *fakeHTTPServer) ListenAndServe() error {
	close(s.started)
	<-s.stopped
	return http.ErrServerClosed
}

func (s *fakeHTTPServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.calls++
	if deadline, ok := ctx.Deadline(); ok {
		s.timeout = time.Until(deadline)
	}
	s.mu.Unlock()
	s.once.Do(func() { close(s.stopped) })
	return nil
}

func (s *fakeHTTPServer) shutdownState() (int, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.timeout
}
