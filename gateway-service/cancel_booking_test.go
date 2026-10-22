package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestGatewayCancellationWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name, fail string
		status     int
		want       []string
	}{
		{"success", "", 204, []string{"read", "payment", "cancel", "decrement"}},
		{"other owner", "owner", 404, []string{"read"}},
		{"missing reservation", "missing", 404, []string{"read"}},
		{"read failure", "read", 503, []string{"read"}},
		{"payment failure", "payment", 503, []string{"read", "payment"}},
		{"loyalty failure", "decrement", 204, []string{"read", "payment", "cancel", "decrement"}},
		{"reservation failure", "cancel", 503, []string{"read", "payment", "cancel", "read"}},
		{"lost cancel response", "lost response", 204, []string{"read", "payment", "cancel", "read", "decrement"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			state := "PAID"
			count := 26
			paymentCanceled := false
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var step string
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/reservations/" + bookingReservationUID:
					step = "read"
				case "DELETE /api/v1/payments/" + bookingPaymentUID:
					step = "payment"
				case "DELETE /api/v1/reservations/" + bookingReservationUID:
					step = "cancel"
				case "PATCH /api/v1/loyalty":
					var input map[string]int
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					if input["reservationCountChange"] == -1 {
						step = "decrement"
					} else if input["reservationCountChange"] == 1 {
						step = "restore"
					} else {
						t.Error("invalid loyalty change")
					}
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				calls = append(calls, step)
				if step != "payment" && testSubjectFromAuthorization(r.Header.Get("Authorization")) != "Test Max" {
					t.Error("missing user header")
				}
				if step == "read" && (tc.fail == "owner" || tc.fail == "missing") {
					w.WriteHeader(404)
					return
				}
				if step == tc.fail {
					w.WriteHeader(500)
					return
				}
				if step == "cancel" && tc.fail == "lost response" {
					state = "CANCELED"
					w.WriteHeader(500)
					return
				}
				switch step {
				case "read":
					json.NewEncoder(w).Encode(map[string]string{"status": state, "paymentUid": bookingPaymentUID})
				case "payment":
					paymentCanceled = true
					w.WriteHeader(204)
				case "decrement":
					count--
					json.NewEncoder(w).Encode(map[string]int{"reservationCount": count})
				case "restore":
					count++
					json.NewEncoder(w).Encode(map[string]int{"reservationCount": count})
				case "cancel":
					state = "CANCELED"
					w.WriteHeader(204)
				}
			}))
			defer backend.Close()
			handler, err := newGatewayHandler(backend.URL, backend.URL, backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer handler.(*gatewayHandler).Close()
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/reservations/"+bookingReservationUID, nil)
			req = testIdentityRequest(req, "Test Max")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("calls = %v, want %v", calls, tc.want)
			}
			if tc.status == 204 {
				wantCount := 25
				if tc.fail == "decrement" {
					wantCount = 26
				}
				if count != wantCount || state != "CANCELED" || !paymentCanceled || response.Body.Len() != 0 {
					t.Fatalf("count=%d state=%s paymentCanceled=%v", count, state, paymentCanceled)
				}
				calls = nil
				again := httptest.NewRecorder()
				handler.ServeHTTP(again, req.Clone(req.Context()))
				if again.Code != 204 || count != wantCount || !reflect.DeepEqual(calls, []string{"read"}) {
					t.Fatalf("repeat: status=%d count=%d calls=%v", again.Code, count, calls)
				}
			} else if count != 26 || state != "PAID" {
				t.Fatalf("failed cancellation: count=%d state=%s", count, state)
			}
		})
	}
}

func TestGatewayCancellationValidation(t *testing.T) {
	handler, err := newGatewayHandler("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ username, uid string }{
		{"", bookingReservationUID}, {"   ", bookingReservationUID}, {strings.Repeat("a", 81), bookingReservationUID}, {"Test Max", "invalid"},
	} {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/reservations/"+tc.uid, nil)
		req = testIdentityRequest(req, tc.username)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 400 {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	}
}

func TestGatewayConcurrentCancellation(t *testing.T) {
	var stateMu sync.Mutex
	state := "PAID"
	count := 26
	paymentCalls, decrementCalls := 0, 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stateMu.Lock()
		defer stateMu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/reservations/" + bookingReservationUID:
			json.NewEncoder(w).Encode(map[string]string{"status": state, "paymentUid": bookingPaymentUID})
		case "DELETE /api/v1/payments/" + bookingPaymentUID:
			paymentCalls++
			w.WriteHeader(204)
		case "PATCH /api/v1/loyalty":
			count--
			decrementCalls++
			w.WriteHeader(200)
		case "DELETE /api/v1/reservations/" + bookingReservationUID:
			state = "CANCELED"
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer backend.Close()
	handler, err := newGatewayHandler(backend.URL, backend.URL, backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			uid := bookingReservationUID
			if index%2 == 0 {
				uid = strings.ToUpper(uid)
			}
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/reservations/"+uid, nil)
			req = testIdentityRequest(req, "Test Max")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != 204 {
				t.Errorf("status = %d, want 204", response.Code)
			}
		}(i)
	}
	workers.Wait()
	if count != 25 || paymentCalls != 1 || decrementCalls != 1 || state != "CANCELED" {
		t.Fatalf("count=%d paymentCalls=%d decrementCalls=%d state=%s", count, paymentCalls, decrementCalls, state)
	}
}
