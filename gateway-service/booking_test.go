package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const bookingHotelUID = "049161bb-badd-4fa8-9d90-87c9a82b0668"
const bookingPaymentUID = "ef714c74-0d27-4ea6-8165-32b6a7b506d2"
const bookingReservationUID = "69ee7fb0-4edd-40a7-93a7-96428819c79a"
const validBookingBody = `{"hotelUid":"049161bb-badd-4fa8-9d90-87c9a82b0668","startDate":"2026-10-10","endDate":"2026-10-13"}`

func TestGatewayBookingWorkflow(t *testing.T) {
	tests := []struct {
		name, fail string
		status     int
		want       []string
	}{
		{"success", "", 200, []string{"hotel", "loyalty", "payment", "reservation", "increment"}},
		{"missing hotel", "hotel", 404, []string{"hotel"}},
		{"loyalty unavailable", "loyalty", 503, []string{"hotel", "loyalty"}},
		{"payment unavailable", "payment", 503, []string{"hotel", "loyalty", "payment"}},
		{"reservation failure", "reservation", 503, []string{"hotel", "loyalty", "payment", "reservation", "cancel payment"}},
		{"loyalty update failure", "increment", 503, []string{"hotel", "loyalty", "payment", "reservation", "increment", "cancel reservation", "cancel payment"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := r.Method + " " + r.URL.Path
				var step string
				var result any
				switch key {
				case "GET /api/v1/hotels/" + bookingHotelUID:
					step, result = "hotel", map[string]int{"price": 10000}
				case "GET /api/v1/loyalty":
					step, result = "loyalty", map[string]int{"discount": 10}
				case "POST /api/v1/payments":
					step = "payment"
					var body struct {
						Price int `json:"price"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Price != 27000 {
						t.Errorf("payment price: %+v, err=%v", body, err)
					}
					result = bookingPayment{UID: bookingPaymentUID, Status: "PAID", Price: 27000}
				case "POST /api/v1/reservations":
					step = "reservation"
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					want := map[string]string{"hotelUid": bookingHotelUID, "paymentUid": bookingPaymentUID, "startDate": "2026-10-10", "endDate": "2026-10-13"}
					if !reflect.DeepEqual(body, want) {
						t.Errorf("reservation body = %v", body)
					}
					result = map[string]string{"reservationUid": bookingReservationUID, "status": "PAID"}
				case "PATCH /api/v1/loyalty":
					step = "increment"
					var body map[string]int
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["reservationCountChange"] != 1 {
						t.Errorf("wrong increment: %v", body)
					}
					result = map[string]int{"reservationCount": 26}
				case "DELETE /api/v1/reservations/" + bookingReservationUID:
					step = "cancel reservation"
				case "DELETE /api/v1/payments/" + bookingPaymentUID:
					step = "cancel payment"
				default:
					t.Errorf("unexpected request: %s", key)
					w.WriteHeader(500)
					return
				}
				if step == "loyalty" || step == "increment" || step == "reservation" || step == "cancel reservation" {
					if r.Header.Get("X-User-Name") != "Test Max" {
						t.Error("missing user header")
					}
				}
				calls = append(calls, step)
				if step == tc.fail {
					if step == "hotel" {
						w.WriteHeader(404)
					} else {
						w.WriteHeader(500)
					}
					return
				}
				if strings.HasPrefix(step, "cancel") {
					w.WriteHeader(204)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if step == "payment" || step == "reservation" {
					w.WriteHeader(201)
				}
				json.NewEncoder(w).Encode(result)
			}))
			defer backend.Close()
			handler, err := newGatewayHandler(backend.URL, backend.URL, backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/reservations", strings.NewReader(validBookingBody))
			req.Header.Set("X-User-Name", "Test Max")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("calls = %v, want %v", calls, tc.want)
			}
			if tc.status == 200 {
				var got bookingResponse
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				want := bookingResponse{bookingReservationUID, bookingHotelUID, "2026-10-10", "2026-10-13", 10, "PAID", bookingPayment{Status: "PAID", Price: 27000}}
				if got != want {
					t.Fatalf("response = %+v, want %+v", got, want)
				}
				if response.Header().Get("Location") != "/api/v1/reservations/"+bookingReservationUID {
					t.Fatal("incorrect Location")
				}
				if strings.Contains(response.Body.String(), "paymentUid") {
					t.Fatal("internal payment UID in public response")
				}
			}
		})
	}
}

func TestGatewayBookingValidation(t *testing.T) {
	handler, err := newGatewayHandler("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, user, body string }{
		{"missing user", "", validBookingBody},
		{"long user", strings.Repeat("a", 81), validBookingBody},
		{"malformed JSON", "Test Max", "{"},
		{"null request", "Test Max", "null"},
		{"extra JSON", "Test Max", validBookingBody + "{}"},
		{"unknown field", "Test Max", strings.TrimSuffix(validBookingBody, "}") + `,"price":1}`},
		{"bad UUID", "Test Max", strings.Replace(validBookingBody, bookingHotelUID, "bad", 1)},
		{"bad date", "Test Max", strings.Replace(validBookingBody, "2026-10-10", "2026-02-30", 1)},
		{"same dates", "Test Max", strings.Replace(validBookingBody, "2026-10-13", "2026-10-10", 1)},
		{"reversed dates", "Test Max", strings.Replace(validBookingBody, "2026-10-13", "2026-10-09", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/reservations", strings.NewReader(tc.body))
			req.Header.Set("X-User-Name", tc.user)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != 400 {
				t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBookingPrice(t *testing.T) {
	for _, tc := range []struct {
		name            string
		nights, nightly int64
		discount        int
		want            int64
		bad             bool
	}{
		{"gold", 3, 10000, 10, 27000, false},
		{"silver", 3, 10000, 7, 27900, false},
		{"bronze", 3, 10000, 5, 28500, false},
		{"round down", 1, 101, 7, 93, false},
		{"overflow", 2, 1 << 62, 10, 0, true},
		{"payment limit", 3, 2147483647, 10, 0, true},
		{"negative price", 3, -1, 10, 0, true},
		{"invalid discount", 3, 10000, 101, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bookingPrice(tc.nights, tc.nightly, tc.discount)
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("price = %d, err = %v", got, err)
			}
		})
	}
}

func TestBookingNightsLongRange(t *testing.T) {
	nights, err := bookingNights(bookingRequest{bookingHotelUID, "1000-01-01", "2000-01-01"})
	if err != nil || nights != 365242 {
		t.Fatalf("nights = %d, err = %v", nights, err)
	}
}

func TestGatewayRejectsInvalidPaymentURL(t *testing.T) {
	for _, value := range []string{"", "payment-service:8060", "ftp://payment-service:8060"} {
		if _, err := newGatewayHandler("http://127.0.0.1:1", "http://127.0.0.1:1", value); err == nil {
			t.Errorf("accepted invalid payment URL %q", value)
		}
	}
}
