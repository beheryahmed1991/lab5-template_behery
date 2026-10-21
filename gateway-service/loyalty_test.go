package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayForwardsLoyaltyToCorrectService(t *testing.T) {
	reservationBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("loyalty request reached Reservation Service")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer reservationBackend.Close()
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const body = `{"status":"GOLD","discount":10,"reservationCount":25}`
			loyaltyBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/loyalty" || r.Header.Get("X-User-Name") != "Test Max" {
					t.Errorf("unexpected request: %s %s user=%q", r.Method, r.URL, r.Header.Get("X-User-Name"))
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				io.WriteString(w, body)
			}))
			defer loyaltyBackend.Close()
			handler, err := newGatewayHandler(reservationBackend.URL, loyaltyBackend.URL, "http://127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/loyalty", nil)
			req.Header.Set("X-User-Name", "Test Max")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != status || response.Body.String() != body || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestGatewayLoyaltyUnavailable(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	backend.Close()
	handler, err := newGatewayHandler("http://127.0.0.1:1", backend.URL, "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/loyalty", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "Loyalty Service unavailable") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestGatewayRejectsInvalidLoyaltyURL(t *testing.T) {
	for _, value := range []string{"", "loyalty-service:8050", "ftp://loyalty-service:8050", "://invalid"} {
		if _, err := newGatewayHandler("http://127.0.0.1:1", value, "http://127.0.0.1:1"); err == nil {
			t.Errorf("accepted invalid loyalty URL %q", value)
		}
	}
}

func TestGatewayLoyaltyDoesNotExposeUpdates(t *testing.T) {
	handler, err := newGatewayHandler("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/v1/loyalty", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.Code)
	}
}
