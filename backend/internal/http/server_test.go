package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajuputra/keluarberapa/backend/internal/logging"
)

func listenLocal(t *testing.T) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

// A deploy must not cut a request in half: a request already running when the
// shutdown signal arrives has to finish.
func TestServeDrainsInFlightRequests(t *testing.T) {
	const handlerWork = 300 * time.Millisecond

	var completed atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(handlerWork)
		completed.Store(true)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	})

	ln := listenLocal(t)
	addr := ln.Addr().String()
	srv := &http.Server{Handler: handler}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := make(chan error, 1)
	go func() { served <- Serve(ctx, srv, ln, 5*time.Second, logging.Discard()) }()

	// Start a request, then signal shutdown while it is still running.
	responded := make(chan *http.Response, 1)
	requestErr := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow") //nolint:noctx // test client
		if err != nil {
			requestErr <- err
			return
		}
		responded <- resp
	}()

	time.Sleep(handlerWork / 3) // let the handler get going
	cancel()

	select {
	case resp := <-responded:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("in-flight request got %d, want %d", resp.StatusCode, http.StatusOK)
		}
	case err := <-requestErr:
		t.Fatalf("in-flight request was cut off: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight request never completed")
	}

	if !completed.Load() {
		t.Error("the handler did not run to completion")
	}

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve returned %v, want nil after a clean drain", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}

	// The listener must be closed, or a restarted process could not rebind.
	if _, err := http.Get("http://" + addr + "/slow"); err == nil { //nolint:noctx // test client
		t.Error("the server still accepts connections after shutdown")
	}
}

// Serve must return once the listener is gone, not hang forever.
func TestServeReturnsWhenTheListenerFails(t *testing.T) {
	ln := listenLocal(t)
	srv := &http.Server{Handler: http.NotFoundHandler()}

	served := make(chan error, 1)
	go func() { served <- Serve(context.Background(), srv, ln, time.Second, logging.Discard()) }()

	// Closing the listener under the server is how a fatal accept error looks.
	time.Sleep(50 * time.Millisecond)
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	select {
	case err := <-served:
		if err == nil {
			t.Error("Serve returned nil, want the accept failure reported")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the listener failed")
	}
}

// A handler that outlasts the grace period must not hang the process: Serve
// reports the timeout and closes what is left.
func TestServeReportsWhenDrainingTimesOut(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	})

	ln := listenLocal(t)
	addr := ln.Addr().String()
	srv := &http.Server{Handler: handler}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := make(chan error, 1)
	go func() { served <- Serve(ctx, srv, ln, 50*time.Millisecond, logging.Discard()) }()

	go func() {
		resp, err := http.Get("http://" + addr + "/stuck") //nolint:noctx // test client
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	time.Sleep(150 * time.Millisecond) // let the request reach the handler
	cancel()

	select {
	case err := <-served:
		if err == nil {
			t.Error("Serve returned nil, want the drain timeout reported")
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Serve returned %v, want a deadline-exceeded error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after the grace period elapsed")
	}
}

// Cancelling before Serve starts must still shut down cleanly.
func TestServeWithAlreadyCancelledContext(t *testing.T) {
	ln := listenLocal(t)
	srv := &http.Server{Handler: http.NotFoundHandler()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, ln, time.Second, logging.Discard()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return for an already-cancelled context")
	}
}
