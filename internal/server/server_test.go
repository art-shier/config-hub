package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
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
	server := New(immediateHTTPServer{err: http.ErrServerClosed},
		WithUserReloader(&fakeReloader{}),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	if err := server.Run(context.Background()); !errors.Is(err, ErrHTTPServe) {
		t.Fatalf("error=%v, want ErrHTTPServe", err)
	}
	if server.Live() || server.Ready() {
		t.Fatalf("failed state live=%v ready=%v", server.Live(), server.Ready())
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

func (s immediateHTTPServer) ListenAndServe() error        { return s.err }
func (immediateHTTPServer) Shutdown(context.Context) error { return nil }

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
