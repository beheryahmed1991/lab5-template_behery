package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	readFailureThreshold = 3
	readRecoveryTimeout  = 10 * time.Second
)

var errCircuitOpen = errors.New("service read circuit is open")

// A shared transport protects direct proxy reads and composed Gateway reads.
// Writes bypass the breaker so recovery and compensation remain possible.
type readBreakerTransport struct {
	base      http.RoundTripper
	mu        sync.Mutex
	circuits  map[string]*readCircuit
	threshold int
	timeout   time.Duration
	now       func() time.Time
}

type readCircuit struct {
	mu         sync.Mutex
	failures   int
	openUntil  time.Time
	probing    bool
	generation uint64
}

type readPermit struct {
	generation uint64
	probe      bool
}

func newReadBreakerTransport(base http.RoundTripper) *readBreakerTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &readBreakerTransport{
		base: base, circuits: make(map[string]*readCircuit),
		threshold: readFailureThreshold, timeout: readRecoveryTimeout, now: time.Now,
	}
}

func (transport *readBreakerTransport) circuit(key string) *readCircuit {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	circuit := transport.circuits[key]
	if circuit == nil {
		circuit = &readCircuit{}
		transport.circuits[key] = circuit
	}
	return circuit
}

func (circuit *readCircuit) acquire(now time.Time) (readPermit, error) {
	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	if !circuit.openUntil.IsZero() {
		if now.Before(circuit.openUntil) || circuit.probing {
			return readPermit{}, errCircuitOpen
		}
		circuit.probing = true
		return readPermit{generation: circuit.generation, probe: true}, nil
	}
	return readPermit{generation: circuit.generation}, nil
}

func (circuit *readCircuit) complete(permit readPermit, failed, canceled bool, now time.Time, threshold int, timeout time.Duration, key string) {
	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	// Ignore older requests that finish after the circuit has changed state.
	if permit.generation != circuit.generation {
		return
	}
	if permit.probe {
		circuit.probing = false
		circuit.generation++
		if failed || canceled {
			circuit.openUntil = now.Add(timeout)
			return
		}
		circuit.failures = 0
		circuit.openUntil = time.Time{}
		log.Printf("Read circuit closed after recovery: %s", key)
		return
	}
	if canceled {
		return
	}
	if !failed {
		circuit.failures = 0
		return
	}
	circuit.failures++
	if circuit.failures >= threshold {
		circuit.openUntil = now.Add(timeout)
		circuit.generation++
		log.Printf("Read circuit opened for %s after %d failures; retry in %s", key, threshold, timeout)
	}
}

func (transport *readBreakerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return transport.base.RoundTrip(request)
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	key := request.URL.Scheme + "://" + request.URL.Host
	circuit := transport.circuit(key)
	permit, err := circuit.acquire(transport.now())
	if err != nil {
		return nil, err
	}
	response, err := transport.base.RoundTrip(request)
	failed := err != nil || (response != nil && response.StatusCode >= http.StatusInternalServerError)
	canceled := errors.Is(request.Context().Err(), context.Canceled)
	circuit.complete(permit, failed, canceled, transport.now(), transport.threshold, transport.timeout, key)
	return response, err
}
