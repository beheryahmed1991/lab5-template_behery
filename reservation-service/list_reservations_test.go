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

type fakeReservationLister struct {
	items    []Reservation
	err      error
	calls    int
	username string
}

func (store *fakeReservationLister) ListReservations(_ context.Context, username string) ([]Reservation, error) {
	store.calls++
	store.username = username
	if store.err != nil {
		return nil, store.err
	}
	var result []Reservation
	for _, item := range store.items {
		if item.Username == username {
			result = append(result, item)
		}
	}
	return result, nil
}

func TestListReservationsHandler(t *testing.T) {
	owned := Reservation{ReservationUID: "69ee7fb0-4edd-40a7-93a7-96428819c79a", Username: "Test Max", Status: "PAID", StartDate: "2026-10-10", EndDate: "2026-10-13"}
	other := Reservation{ReservationUID: "a0425f4b-9211-4d95-8060-de6081400740", Username: "Other User", Status: "CANCELED"}
	tests := []struct {
		name, username       string
		err                  error
		status, calls, count int
	}{
		{"own reservations", "Test Max", nil, 200, 1, 1},
		{"other user", "Other User", nil, 200, 1, 1},
		{"empty list", "New User", nil, 200, 1, 0},
		{"missing username", "", nil, 400, 0, 0},
		{"blank username", "   ", nil, 400, 0, 0},
		{"long username", strings.Repeat("a", 81), nil, 400, 0, 0},
		{"database failure", "Test Max", errors.New("secret database details"), 500, 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeReservationLister{items: []Reservation{owned, other}, err: tc.err}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/reservations", listReservationsHandler(store))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/reservations", nil)
			req.Header.Set("X-User-Name", tc.username)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, req)
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.status, recorder.Body.String())
			}
			if store.calls != tc.calls {
				t.Fatalf("calls = %d, want %d", store.calls, tc.calls)
			}
			if store.calls > 0 && store.username != tc.username {
				t.Fatal("wrong username passed to store")
			}
			if strings.Contains(recorder.Body.String(), "secret") {
				t.Fatal("database details leaked")
			}
			if tc.status == 200 {
				var got []Reservation
				if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got == nil {
					t.Fatal("expected JSON array, received null")
				}
				if len(got) != tc.count {
					t.Fatalf("count = %d, want %d", len(got), tc.count)
				}
				for _, item := range got {
					if item.Username != tc.username {
						t.Fatal("another user's reservation returned")
					}
					if item != owned && item != other {
						t.Fatal("reservation data changed")
					}
				}
			}
		})
	}
}
