package server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func TestHandlerTrackerRejectsRequestsAfterDrainBegins(t *testing.T) {
	var calls atomic.Int32
	tracker := NewHandlerTracker(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	tracker.BeginDrain()

	response := httptest.NewRecorder()
	tracker.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("underlying handler calls=%d, want 0", got)
	}
}

func TestHandlerTrackerWaitsForAdmittedHandlers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		tracker := NewHandlerTracker(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(entered)
			<-release
		}))

		requestDone := make(chan struct{})
		go func() {
			tracker.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			close(requestDone)
		}()
		<-entered

		tracker.BeginDrain()
		waitDone := make(chan struct{})
		go func() {
			tracker.Wait()
			close(waitDone)
		}()
		synctest.Wait()

		returnedEarly := false
		select {
		case <-waitDone:
			returnedEarly = true
		default:
		}

		close(release)
		<-requestDone
		<-waitDone
		if returnedEarly {
			t.Fatal("Wait returned before the admitted handler completed")
		}
	})
}
