package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeReservationReader struct {
	result        Reservation
	err           error
	calls         int
	username, uid string
}

func (store *fakeReservationReader) GetReservation(_ context.Context, username, uid string) (Reservation, error) {
	store.calls++
	store.username, store.uid = username, uid
	if store.err != nil {
		return Reservation{}, store.err
	}
	if username != store.result.Username || uid != store.result.ReservationUID {
		return Reservation{}, errReservationNotFound
	}
	return store.result, nil
}

func TestGetReservationHandler(t *testing.T) {
	const uid = "69ee7fb0-4edd-40a7-93a7-96428819c79a"
	reservation := Reservation{ReservationUID: uid, Username: "Test Max", HotelUID: "049161bb-badd-4fa8-9d90-87c9a82b0668", PaymentUID: "ef714c74-0d27-4ea6-8165-32b6a7b506d2", Status: "PAID", StartDate: "2026-10-10", EndDate: "2026-10-13"}
	tests := []struct {
		name, username, uid string
		err                 error
		status, calls       int
	}{
		{"success", "Test Max", uid, nil, 200, 1},
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
			store := &fakeReservationReader{result: reservation, err: tc.err}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/reservations/{reservationUid}", getReservationHandler(store))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/reservations/"+tc.uid, nil)
			req.Header.Set("X-User-Name", tc.username)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, req)
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.status, recorder.Body.String())
			}
			if store.calls != tc.calls {
				t.Fatalf("store calls = %d, want %d", store.calls, tc.calls)
			}
			if store.calls > 0 && (store.username != tc.username || store.uid != tc.uid) {
				t.Fatal("incorrect owner or reservation passed to store")
			}
			if strings.Contains(recorder.Body.String(), "secret") {
				t.Fatal("database details leaked")
			}
			if tc.status == 200 {
				var got Reservation
				if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got != reservation {
					t.Fatalf("reservation = %+v, want %+v", got, reservation)
				}
			}
		})
	}
}
