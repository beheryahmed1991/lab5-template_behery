package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestGatewayUserInfo(t *testing.T) {
	for _, tc := range []struct {
		name, fail, user string
		empty            bool
		status           int
	}{
		{"success", "", "Test Max", false, 200},
		{"new user", "", "New User", true, 200},
		{"reservation unavailable", "reservations", "Test Max", false, 503},
		{"payment unavailable", "payment", "Test Max", false, 200},
		{"loyalty unavailable", "loyalty", "Test Max", false, 200},
		{"malformed loyalty", "malformed", "Test Max", false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loyaltyCalls := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("user information must use GET")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/reservations":
					if testSubjectFromAuthorization(r.Header.Get("Authorization")) != tc.user {
						t.Error("wrong reservation username")
					}
					if tc.fail == "reservations" {
						w.WriteHeader(500)
						return
					}
					if tc.empty {
						json.NewEncoder(w).Encode([]internalReservation{})
						return
					}
					json.NewEncoder(w).Encode([]internalReservation{{bookingReservationUID, tc.user, bookingHotelUID, bookingPaymentUID, "CANCELED", "2026-10-10", "2026-10-13"}})
				case "/api/v1/hotels/" + bookingHotelUID:
					json.NewEncoder(w).Encode(map[string]any{"hotelUid": bookingHotelUID, "name": "Ararat Park Hyatt Moscow", "country": "Россия", "city": "Москва", "address": "Неглинная ул., 4", "stars": 5})
				case "/api/v1/payments/" + bookingPaymentUID:
					if tc.fail == "payment" {
						w.WriteHeader(500)
						return
					}
					json.NewEncoder(w).Encode(bookingPayment{UID: bookingPaymentUID, Status: "CANCELED", Price: 27000})
				case "/api/v1/loyalty":
					loyaltyCalls++
					if testSubjectFromAuthorization(r.Header.Get("Authorization")) != tc.user {
						t.Error("wrong loyalty username")
					}
					if tc.fail == "loyalty" {
						w.WriteHeader(500)
						return
					}
					if tc.fail == "malformed" {
						w.Write([]byte("{"))
						return
					}
					result := loyaltyInfo{"GOLD", 10, 25}
					if tc.empty {
						result = loyaltyInfo{"BRONZE", 5, 0}
					}
					json.NewEncoder(w).Encode(result)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer backend.Close()
			handler, err := newGatewayHandler(backend.URL, backend.URL, backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			req = testIdentityRequest(req, tc.user)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if tc.fail == "reservations" && loyaltyCalls != 0 {
				t.Fatal("requested loyalty after reservation failure")
			}
			if tc.status != 200 {
				return
			}
			var got userInfo
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Reservations == nil {
				t.Fatal("reservations must be a JSON array")
			}
			if tc.empty {
				if len(got.Reservations) != 0 || got.Loyalty != (loyaltyInfo{"BRONZE", 5, 0}) {
					t.Fatalf("new user = %+v", got)
				}
			} else {
				stars := 5
				want := userInfo{[]reservationInfo{{bookingReservationUID, hotelInfo{bookingHotelUID, "Ararat Park Hyatt Moscow", "Россия, Москва, Неглинная ул., 4", &stars}, "2026-10-10", "2026-10-13", "CANCELED", bookingPayment{Status: "CANCELED", Price: 27000}}}, loyaltyInfo{"GOLD", 10, 25}}
				if tc.fail == "payment" {
					want.Reservations[0].Payment = bookingPayment{}
					if !strings.Contains(response.Body.String(), `"payment":{}`) {
						t.Fatal("payment fallback must be an empty object")
					}
				}
				if tc.fail == "loyalty" || tc.fail == "malformed" {
					want.Loyalty = loyaltyInfo{}
					if !strings.Contains(response.Body.String(), `"loyalty":{}`) {
						t.Fatal("loyalty fallback must be an empty object")
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("user information = %+v, want %+v", got, want)
				}
			}
			if strings.Contains(response.Body.String(), "paymentUid") || strings.Contains(response.Body.String(), "username") {
				t.Fatal("internal fields exposed")
			}
		})
	}
}

func TestGatewayUserInfoValidation(t *testing.T) {
	handler, err := newGatewayHandler("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, username := range []string{"", "   ", strings.Repeat("a", 81)} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		req = testIdentityRequest(req, username)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 400 {
			t.Fatalf("status=%d, want 400", response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/me", nil))
	if response.Code != 405 {
		t.Fatalf("POST status=%d, want 405", response.Code)
	}
}
