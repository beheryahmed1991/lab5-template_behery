package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCancellationRetryRecovery(t *testing.T) {
	for _, lostResponse := range []bool{false, true} {
		name := "service unavailable"
		if lostResponse {
			name = "response lost after commit"
		}
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			available := false
			state, count, payments, attempts := "PAID", 26, 0, 0
			seen := make(map[string]bool)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/reservations/" + bookingReservationUID:
					json.NewEncoder(w).Encode(map[string]string{"status": state, "paymentUid": bookingPaymentUID})
				case "DELETE /api/v1/payments/" + bookingPaymentUID:
					payments++
					w.WriteHeader(204)
				case "DELETE /api/v1/reservations/" + bookingReservationUID:
					state = "CANCELED"
					w.WriteHeader(204)
				case "PATCH /api/v1/loyalty":
					attempts++
					key := r.Header.Get("Idempotency-Key")
					if key != "cancel:"+bookingReservationUID {
						t.Errorf("unexpected key %q", key)
					}
					if r.Header.Get("X-User-Name") != "Test Max" {
						t.Error("missing user")
					}
					var body map[string]int
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["reservationCountChange"] != -1 {
						t.Error("invalid update")
					}
					// Model a transaction that committed while its response was lost.
					if (available || lostResponse && attempts == 1) && !seen[key] {
						count--
						seen[key] = true
					}
					if !available {
						w.WriteHeader(503)
						return
					}
					w.WriteHeader(200)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer backend.Close()
			raw, err := newGatewayHandler(backend.URL, backend.URL, backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			handler := raw.(*gatewayHandler)
			defer handler.Close()
			// Configure before the lazy worker starts.
			handler.retries.interval = 15 * time.Millisecond
			request := httptest.NewRequest(http.MethodDelete, "/api/v1/reservations/"+bookingReservationUID, nil)
			request.Header.Set("X-User-Name", "Test Max")
			for i := 0; i < 2; i++ {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request.Clone(request.Context()))
				if response.Code != 204 {
					t.Fatalf("cancel returned %d: %s", response.Code, response.Body.String())
				}
			}
			// The worker must keep trying while the service remains unavailable.
			awaitRetry(t, func() bool { mu.Lock(); defer mu.Unlock(); return attempts >= 3 })
			handler.retries.mu.Lock()
			queued := len(handler.retries.jobs)
			handler.retries.mu.Unlock()
			if queued != 1 {
				t.Fatalf("pending jobs = %d, want 1", queued)
			}
			mu.Lock()
			if state != "CANCELED" || payments != 1 {
				t.Errorf("state=%s payments=%d", state, payments)
			}
			available = true
			mu.Unlock()
			awaitRetry(t, func() bool {
				handler.retries.mu.Lock()
				defer handler.retries.mu.Unlock()
				return len(handler.retries.jobs) == 0
			})
			handler.Close()
			mu.Lock()
			defer mu.Unlock()
			if count != 25 || len(seen) != 1 {
				t.Fatalf("count=%d applied operations=%d", count, len(seen))
			}
		})
	}
}

func awaitRetry(t *testing.T, predicate func() bool) {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-timeout.C:
			t.Fatal("timed out waiting for retry worker")
		case <-tick.C:
		}
	}
}

func TestRetryQueueDeduplicatesAndStops(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer backend.Close()
	client := bookingClient{loyaltyURL: backend.URL, http: backend.Client()}
	queue := newLoyaltyRetryQueue(client, 5*time.Millisecond)
	defer queue.Close()
	job := loyaltyRetryJob{username: "Test Max", reservationUID: bookingReservationUID}
	for i := 0; i < 5; i++ {
		if err := queue.enqueue(job); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("retry never started")
	}
	queue.mu.Lock()
	count := len(queue.jobs)
	queue.mu.Unlock()
	if count != 1 {
		t.Fatalf("jobs=%d", count)
	}
	queue.Close()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("request not canceled")
	}
	if err := queue.enqueue(job); err == nil {
		t.Fatal("closed queue accepted job")
	}
	if queue.ctx.Err() != context.Canceled {
		t.Fatal("worker context not canceled")
	}
}
