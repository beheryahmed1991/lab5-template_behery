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

func validReservationRequest() createReservationRequest {
	return createReservationRequest{HotelUID: "049161bb-badd-4fa8-9d90-87c9a82b0668", PaymentUID: "08fb7dad-63a3-40b3-9f2e-10dc1ea0985e", StartDate: "2026-10-10", EndDate: "2026-10-13"}
}
func TestReservationValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		modify func(*createReservationRequest)
	}{
		{"invalid hotel UUID", func(r *createReservationRequest) { r.HotelUID = "invalid" }},
		{"missing payment", func(r *createReservationRequest) { r.PaymentUID = "" }},
		{"impossible date", func(r *createReservationRequest) { r.StartDate = "2026-02-30" }},
		{"wrong date format", func(r *createReservationRequest) { r.EndDate = "13/10/2026" }},
		{"equal dates", func(r *createReservationRequest) { r.EndDate = r.StartDate }},
		{"reversed dates", func(r *createReservationRequest) { r.EndDate = "2026-10-09" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := validReservationRequest()
			tt.modify(&request)
			if validateReservationRequest(&request) == nil {
				t.Fatal("invalid reservation must be rejected")
			}
		})
	}
	request := validReservationRequest()
	if err := validateReservationRequest(&request); err != nil {
		t.Fatal(err)
	}
	if request.end.Sub(request.start).Hours() != 72 {
		t.Fatal("expected three nights")
	}
}

type fakeReservationCreator struct {
	calls    int
	username string
	request  createReservationRequest
	err      error
}

func (store *fakeReservationCreator) CreateReservation(ctx context.Context, username string, request createReservationRequest) (Reservation, error) {
	store.calls++
	store.username = username
	store.request = request
	if _, ok := ctx.Deadline(); !ok {
		return Reservation{}, errors.New("missing timeout")
	}
	return Reservation{ReservationUID: "049161bb-badd-4fa8-9d90-87c9a82b0668", Username: username, HotelUID: request.HotelUID, PaymentUID: request.PaymentUID, StartDate: request.StartDate, EndDate: request.EndDate, Status: "PAID"}, store.err
}
func TestCreateReservationResponses(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
	}{
		{"success", nil, 201}, {"hotel not found", errHotelNotFound, 404}, {"database failure", errors.New("private database details"), 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := validReservationRequest()
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			store := &fakeReservationCreator{err: tt.err}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/reservations", strings.NewReader(string(body)))
			request.Header.Set("X-User-Name", "Test Max")
			response := httptest.NewRecorder()
			createReservationHandler(store).ServeHTTP(response, request)
			if response.Code != tt.status || store.calls != 1 || store.username != "Test Max" {
				t.Fatalf("unexpected response: %d store=%+v", response.Code, store)
			}
			if strings.Contains(response.Body.String(), "private database details") {
				t.Fatal("database details leaked")
			}
			if tt.status == 201 {
				var got Reservation
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Status != "PAID" || got.PaymentUID != input.PaymentUID || got.HotelUID != input.HotelUID || got.StartDate != input.StartDate || got.EndDate != input.EndDate || got.Username != "Test Max" {
					t.Fatalf("unexpected reservation: %+v", got)
				}
				if response.Header().Get("Location") != "/api/v1/reservations/"+got.ReservationUID {
					t.Fatal("missing reservation location")
				}
			}
		})
	}
}
func TestCreateReservationRejectsInvalidRequestsBeforeSaving(t *testing.T) {
	input := validReservationRequest()
	validBody, _ := json.Marshal(input)
	for _, tt := range []struct {
		username string
		body     string
	}{
		{"", string(validBody)}, {"   ", string(validBody)}, {strings.Repeat("x", 81), string(validBody)},
		{"Test Max", `{}`}, {"Test Max", `null`}, {"Test Max", `{"extra":true}`}, {"Test Max", string(validBody) + `{}`},
	} {
		store := &fakeReservationCreator{}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/reservations", strings.NewReader(tt.body))
		request.Header.Set("X-User-Name", tt.username)
		response := httptest.NewRecorder()
		createReservationHandler(store).ServeHTTP(response, request)
		if response.Code != 400 || store.calls != 0 {
			t.Fatalf("invalid input reached database: status=%d calls=%d", response.Code, store.calls)
		}
	}
}
