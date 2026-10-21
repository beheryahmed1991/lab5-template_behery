package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestGatewayReservationInfo(t *testing.T) {
	stars := 5
	owned := internalReservation{bookingReservationUID, "Test Max", bookingHotelUID, bookingPaymentUID, "CANCELED", "2026-10-10", "2026-10-13"}
	want := reservationInfo{bookingReservationUID, hotelInfo{bookingHotelUID, "Ararat Park Hyatt Moscow", "Россия, Москва, Неглинная ул., 4", &stars}, "2026-10-10", "2026-10-13", "CANCELED", bookingPayment{Status: "CANCELED", Price: 27000}}
	for _, tc := range []struct {
		name, path, fail                 string
		status, hotelCalls, paymentCalls int
	}{
		{"single", "/api/v1/reservations/" + bookingReservationUID, "", 200, 1, 1},
		{"list", "/api/v1/reservations", "", 200, 1, 2},
		{"empty list", "/api/v1/reservations", "empty", 200, 0, 0},
		{"missing", "/api/v1/reservations/" + bookingReservationUID, "missing", 404, 0, 0},
		{"wrong owner", "/api/v1/reservations/" + bookingReservationUID, "owner", 404, 0, 0},
		{"wrong owner list", "/api/v1/reservations", "owner", 503, 0, 0},
		{"reservation unavailable", "/api/v1/reservations/" + bookingReservationUID, "reservation", 503, 0, 0},
		{"hotel unavailable", "/api/v1/reservations/" + bookingReservationUID, "hotel", 503, 1, 0},
		{"payment unavailable", "/api/v1/reservations/" + bookingReservationUID, "payment", 200, 1, 1},
		{"list payment unavailable", "/api/v1/reservations", "payment", 200, 1, 2},
		{"malformed reservation", "/api/v1/reservations/" + bookingReservationUID, "malformed", 503, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hotelCalls, paymentCalls := 0, 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected method %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/reservations", "/api/v1/reservations/" + bookingReservationUID:
					if r.Header.Get("X-User-Name") != "Test Max" {
						t.Error("missing username")
					}
					if tc.fail == "missing" {
						w.WriteHeader(404)
						return
					}
					if tc.fail == "reservation" {
						w.WriteHeader(500)
						return
					}
					if tc.fail == "malformed" {
						w.Write([]byte("{"))
						return
					}
					source := owned
					if tc.fail == "owner" {
						source.Username = "Other User"
					}
					if r.URL.Path == "/api/v1/reservations" {
						if tc.fail == "empty" {
							json.NewEncoder(w).Encode([]internalReservation{})
							return
						}
						second := source
						second.ReservationUID = "a0425f4b-9211-4d95-8060-de6081400740"
						json.NewEncoder(w).Encode([]internalReservation{source, second})
					} else {
						json.NewEncoder(w).Encode(source)
					}
				case "/api/v1/hotels/" + bookingHotelUID:
					hotelCalls++
					if tc.fail == "hotel" {
						w.WriteHeader(500)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"hotelUid": bookingHotelUID, "name": "Ararat Park Hyatt Moscow", "country": "Россия", "city": "Москва", "address": "Неглинная ул., 4", "stars": stars, "price": 10000})
				case "/api/v1/payments/" + bookingPaymentUID:
					paymentCalls++
					if tc.fail == "payment" {
						w.WriteHeader(500)
						return
					}
					json.NewEncoder(w).Encode(bookingPayment{UID: bookingPaymentUID, Status: "CANCELED", Price: 27000})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer backend.Close()
			handler, err := newGatewayHandler(backend.URL, "http://127.0.0.1:1", backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("X-User-Name", "Test Max")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if hotelCalls != tc.hotelCalls || paymentCalls != tc.paymentCalls {
				t.Fatalf("hotel calls=%d payment calls=%d", hotelCalls, paymentCalls)
			}
			if tc.status != 200 {
				return
			}
			if strings.Contains(response.Body.String(), "paymentUid") || strings.Contains(response.Body.String(), "username") {
				t.Fatal("internal fields exposed")
			}
			expected := want
			if tc.fail == "payment" {
				expected.Payment = bookingPayment{}
				if !strings.Contains(response.Body.String(), `"payment":{}`) {
					t.Fatal("payment fallback must be an empty object")
				}
			}
			if tc.path == "/api/v1/reservations" {
				var got []reservationInfo
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if tc.fail == "empty" {
					if got == nil || len(got) != 0 {
						t.Fatal("expected empty array")
					}
					return
				}
				second := expected
				second.ReservationUID = "a0425f4b-9211-4d95-8060-de6081400740"
				if !reflect.DeepEqual(got, []reservationInfo{expected, second}) {
					t.Fatalf("list = %+v", got)
				}
			} else {
				var got reservationInfo
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, expected) {
					t.Fatalf("reservation = %+v, want %+v", got, want)
				}
			}
		})
	}
}

func TestGatewayReservationInfoValidation(t *testing.T) {
	handler, err := newGatewayHandler("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, user string }{
		{"/api/v1/reservations", ""},
		{"/api/v1/reservations", strings.Repeat("a", 81)},
		{"/api/v1/reservations/" + bookingReservationUID, "   "},
		{"/api/v1/reservations/invalid", "Test Max"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("X-User-Name", tc.user)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 400 {
			t.Fatalf("status=%d, want 400", response.Code)
		}
	}
}
