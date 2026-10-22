package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUserFallbackSurvivesOpenCircuitsAndRecovers(t *testing.T) {
	reservation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/reservations":
			json.NewEncoder(w).Encode([]internalReservation{{bookingReservationUID, "Test Max", bookingHotelUID, bookingPaymentUID, "PAID", "2026-10-20", "2026-10-23"}})
		case "/api/v1/hotels/" + bookingHotelUID:
			json.NewEncoder(w).Encode(map[string]any{"hotelUid": bookingHotelUID, "name": "Hotel", "stars": 5})
		default:
			t.Errorf("unexpected reservation request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer reservation.Close()
	var recovered atomic.Bool
	var paymentCalls, loyaltyCalls atomic.Int32
	payment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paymentCalls.Add(1)
		if !recovered.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(bookingPayment{UID: bookingPaymentUID, Status: "PAID", Price: 0})
	}))
	defer payment.Close()
	loyalty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loyaltyCalls.Add(1)
		if !recovered.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(loyaltyInfo{"BRONZE", 0, 0})
	}))
	defer loyalty.Close()
	now := time.Unix(100, 0)
	transport := newReadBreakerTransport(nil)
	transport.now = func() time.Time { return now }
	client := bookingClient{reservationURL: reservation.URL, paymentURL: payment.URL, loyaltyURL: loyalty.URL, http: &http.Client{Transport: transport, Timeout: time.Second}}
	read := func() map[string]json.RawMessage {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		request = testIdentityRequest(request, "Test Max")
		response := httptest.NewRecorder()
		client.getUserInfo(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d: %s", response.Code, response.Body.String())
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	for i := 0; i < 4; i++ {
		body := read()
		if string(body["loyalty"]) != "{}" {
			t.Fatalf("loyalty fallback = %s", body["loyalty"])
		}
		var reservations []map[string]json.RawMessage
		if err := json.Unmarshal(body["reservations"], &reservations); err != nil {
			t.Fatal(err)
		}
		if len(reservations) != 1 || string(reservations[0]["payment"]) != "{}" {
			t.Fatalf("reservation fallback = %s", body["reservations"])
		}
		if string(reservations[0]["reservationUid"]) != `"`+bookingReservationUID+`"` {
			t.Fatal("lost reservation data")
		}
	}
	if paymentCalls.Load() != 3 || loyaltyCalls.Load() != 3 {
		t.Fatalf("open circuits reached backends: payment=%d loyalty=%d", paymentCalls.Load(), loyaltyCalls.Load())
	}
	recovered.Store(true)
	now = now.Add(readRecoveryTimeout)
	body := read()
	if string(body["loyalty"]) != `{"status":"BRONZE","discount":0,"reservationCount":0}` {
		t.Fatalf("recovered loyalty = %s", body["loyalty"])
	}
	if !strings.Contains(string(body["reservations"]), `"payment":{"status":"PAID","price":0}`) {
		t.Fatalf("recovered zero payment = %s", body["reservations"])
	}
}

func TestPaymentFallbackOnConnectionAndDecodeFailures(t *testing.T) {
	for _, failure := range []string{"connection", "malformed"} {
		t.Run(failure, func(t *testing.T) {
			payment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Decode may populate status before encountering an invalid price.
				w.Write([]byte(`{"status":"PAID","price":"invalid"}`))
			}))
			defer payment.Close()
			if failure == "connection" {
				payment.Close()
			}
			reservation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/reservations/" + bookingReservationUID:
					json.NewEncoder(w).Encode(internalReservation{bookingReservationUID, "Test Max", bookingHotelUID, bookingPaymentUID, "PAID", "2026-10-20", "2026-10-23"})
				default:
					json.NewEncoder(w).Encode(map[string]any{"hotelUid": bookingHotelUID, "name": "Hotel"})
				}
			}))
			defer reservation.Close()
			handler, err := newGatewayHandler(reservation.URL, "http://127.0.0.1:1", payment.URL)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/reservations/"+bookingReservationUID, nil)
			request = testIdentityRequest(request, "Test Max")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var body map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || string(body["payment"]) != "{}" {
				t.Fatalf("fallback response: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
