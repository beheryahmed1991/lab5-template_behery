package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeReservationCanceler struct {
	reservation   Reservation
	err           error
	calls         int
	username, uid string
}

func (store *fakeReservationCanceler) CancelReservation(_ context.Context, username, uid string) error {
	store.calls++
	store.username, store.uid = username, uid
	if store.err != nil {
		return store.err
	}
	if store.reservation.Username != username || store.reservation.ReservationUID != uid {
		return errReservationNotFound
	}
	store.reservation.Status = "CANCELED"
	return nil
}

func TestCancelReservationHandler(t *testing.T) {
	const uid = "69ee7fb0-4edd-40a7-93a7-96428819c79a"
	tests := []struct {
		name, username, uid string
		err                 error
		status, calls       int
	}{
		{"success", "Test Max", uid, nil, 204, 1},
		{"other owner", "Other User", uid, nil, 404, 1},
		{"missing reservation", "Test Max", "a0425f4b-9211-4d95-8060-de6081400740", nil, 404, 1},
		{"missing username", "", uid, nil, 400, 0},
		{"blank username", "   ", uid, nil, 400, 0},
		{"long username", strings.Repeat("a", 81), uid, nil, 400, 0},
		{"invalid UUID", "Test Max", "invalid", nil, 400, 0},
		{"database failure", "Test Max", uid, errors.New("secret database details"), 500, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := Reservation{ReservationUID: uid, Username: "Test Max", Status: "PAID"}
			store := &fakeReservationCanceler{reservation: original, err: tc.err}
			mux := http.NewServeMux()
			mux.HandleFunc("DELETE /api/v1/reservations/{reservationUid}", cancelReservationHandler(store))
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/reservations/"+tc.uid, nil)
			req.Header.Set("X-User-Name", tc.username)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, req)
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.status, recorder.Body.String())
			}
			if store.calls != tc.calls {
				t.Fatalf("calls = %d, want %d", store.calls, tc.calls)
			}
			if store.calls > 0 && (store.username != tc.username || store.uid != tc.uid) {
				t.Fatal("incorrect owner or reservation passed to store")
			}
			if strings.Contains(recorder.Body.String(), "secret") {
				t.Fatal("database details leaked")
			}
			if tc.status != 204 {
				if store.reservation != original {
					t.Fatal("failed request changed reservation")
				}
				return
			}
			if recorder.Body.Len() != 0 || store.reservation.Status != "CANCELED" {
				t.Fatal("expected empty response and canceled reservation")
			}
			again := httptest.NewRecorder()
			mux.ServeHTTP(again, req.Clone(context.Background()))
			if again.Code != 204 || again.Body.Len() != 0 || store.reservation.Status != "CANCELED" {
				t.Fatal("repeated cancellation must succeed")
			}
		})
	}
}
