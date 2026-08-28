package server

import (
	"net/http"
	"sync"
)

// HandlerTracker rejects new requests once draining begins.
type HandlerTracker struct {
	mu       sync.Mutex
	active   sync.WaitGroup
	draining bool
	handler  http.Handler
}

func NewHandlerTracker(handler http.Handler) *HandlerTracker {
	return &HandlerTracker{handler: handler}
}

func (t *HandlerTracker) BeginDrain() {
	t.mu.Lock()
	t.draining = true
	t.mu.Unlock()
}

func (t *HandlerTracker) Wait() {
	t.active.Wait()
}

func (t *HandlerTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	if t.draining {
		t.mu.Unlock()
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	t.active.Add(1)
	t.mu.Unlock()
	defer t.active.Done()

	t.handler.ServeHTTP(w, r)
}
