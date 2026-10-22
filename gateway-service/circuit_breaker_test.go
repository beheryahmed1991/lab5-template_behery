package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type breakerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f breakerRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func breakerResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}
}

func TestReadBreakerOpensRecoversAndSeparatesServices(t *testing.T) {
	now := time.Unix(100, 0)
	status := http.StatusServiceUnavailable
	calls := make(map[string]int)
	transport := newReadBreakerTransport(breakerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls[r.URL.Host]++
		return breakerResponse(status), nil
	}))
	transport.now = func() time.Time { return now }
	read := func(host, method string) error {
		r := httptest.NewRequest(method, "http://"+host+"/api/v1/loyalty", nil)
		response, err := transport.RoundTrip(r)
		if response != nil {
			response.Body.Close()
		}
		return err
	}
	for i := 0; i < readFailureThreshold; i++ {
		if err := read("loyalty", http.MethodGet); err != nil {
			t.Fatal(err)
		}
	}
	if err := read("loyalty", http.MethodGet); !errors.Is(err, errCircuitOpen) {
		t.Fatalf("fourth read: %v", err)
	}
	if calls["loyalty"] != 3 {
		t.Fatalf("backend calls = %d", calls["loyalty"])
	}
	status = http.StatusOK
	if err := read("payment", http.MethodGet); err != nil {
		t.Fatalf("independent service blocked: %v", err)
	}
	if err := read("loyalty", http.MethodPatch); err != nil {
		t.Fatalf("write blocked: %v", err)
	}
	if err := read("loyalty", http.MethodHead); !errors.Is(err, errCircuitOpen) {
		t.Fatalf("write incorrectly closed read circuit: %v", err)
	}
	now = now.Add(readRecoveryTimeout)
	status = http.StatusServiceUnavailable
	if err := read("loyalty", http.MethodGet); err != nil {
		t.Fatal(err)
	}
	if err := read("loyalty", http.MethodGet); !errors.Is(err, errCircuitOpen) {
		t.Fatalf("failed probe did not reopen: %v", err)
	}
	now = now.Add(readRecoveryTimeout)
	status = http.StatusOK
	if err := read("loyalty", http.MethodGet); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
	if err := read("loyalty", http.MethodGet); err != nil {
		t.Fatalf("circuit not closed: %v", err)
	}
}

func TestReadBreakerCountsNetworkErrorsButNotClientErrors(t *testing.T) {
	status := http.StatusInternalServerError
	networkFailure := false
	transport := newReadBreakerTransport(breakerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if networkFailure {
			return nil, errors.New("connection refused")
		}
		return breakerResponse(status), nil
	}))
	read := func() error {
		response, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "http://service/resource", nil))
		if response != nil {
			response.Body.Close()
		}
		return err
	}
	for i := 0; i < 2; i++ {
		read()
	}
	status = http.StatusNotFound
	if err := read(); err != nil {
		t.Fatal(err)
	}
	networkFailure = true
	for i := 0; i < 3; i++ {
		if err := read(); err == nil || errors.Is(err, errCircuitOpen) {
			t.Fatalf("network failure %d: %v", i, err)
		}
	}
	if err := read(); !errors.Is(err, errCircuitOpen) {
		t.Fatalf("network failures did not open circuit: %v", err)
	}
}

func TestReadBreakerAllowsOnlyOneConcurrentRecoveryProbe(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	transport := newReadBreakerTransport(breakerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-release
		return breakerResponse(http.StatusOK), nil
	}))
	now := time.Unix(100, 0)
	transport.now = func() time.Time { return now }
	circuit := transport.circuit("http://service")
	circuit.openUntil = now.Add(-time.Second)
	done := make(chan error, 1)
	go func() {
		response, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "http://service/a", nil))
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	<-started
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "http://service/b", nil))
			if !errors.Is(err, errCircuitOpen) {
				t.Errorf("concurrent probe allowed: %v", err)
			}
		}()
	}
	group.Wait()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("probe calls = %d", calls.Load())
	}
}

func TestReadBreakerIgnoresOldResponsesAndCanceledCallers(t *testing.T) {
	circuit := &readCircuit{}
	now := time.Unix(100, 0)
	old, _ := circuit.acquire(now)
	for i := 0; i < 3; i++ {
		permit, _ := circuit.acquire(now)
		circuit.complete(permit, true, false, now, 3, time.Second, "service")
	}
	circuit.complete(old, false, false, now, 3, time.Second, "service")
	if _, err := circuit.acquire(now); !errors.Is(err, errCircuitOpen) {
		t.Fatal("old success closed circuit")
	}
	transport := newReadBreakerTransport(breakerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("canceled request reached backend")
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "http://service", nil).WithContext(ctx))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestGatewaySharesBreakerBetweenProxyAndComposedReads(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()
	handler, err := newGatewayHandler(backend.URL, "http://127.0.0.1:1", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/hotels", "/api/v1/reservations", "/api/v1/me", "/api/v1/hotels"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = testIdentityRequest(request, "Test Max")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("backend calls = %d, want 3", calls.Load())
	}
}
