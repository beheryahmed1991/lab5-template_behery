package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayForwardsHotelRequestAndResponse(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const body = `{"page":1,"items":[{"name":"Ararat Park Hyatt Moscow"}]}`
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/hotels" || r.URL.RawQuery != "page=1&size=10" {
					t.Errorf("unexpected forwarded request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				io.WriteString(w, body)
			}))
			defer backend.Close()
			handler, err := newGatewayHandler(backend.URL, "http://127.0.0.1:1", "http://127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/hotels?page=1&size=10", nil))
			if response.Code != status || response.Body.String() != body || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}

func TestGatewayHealthAndRouteRestrictions(t *testing.T) {
	handler, err := newGatewayHandler("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/manage/health", http.StatusOK},
		{http.MethodGet, "/unknown", http.StatusNotFound},
		{http.MethodPost, "/api/v1/hotels", http.StatusMethodNotAllowed},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status {
			t.Errorf("%s %s: got %d, want %d", test.method, test.path, response.Code, test.status)
		}
	}
}

func TestGatewayUnavailableBackend(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	backend.Close()
	handler, err := newGatewayHandler(backend.URL, "http://127.0.0.1:1", "http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/hotels", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "Reservation Service unavailable") {
		t.Fatalf("unexpected failure response: %d %s", response.Code, response.Body.String())
	}
}

func TestGatewayRejectsMissingOrInvalidServiceURL(t *testing.T) {
	for _, value := range []string{"", "reservation-service:8070", "ftp://reservation-service:8070", "://invalid"} {
		if _, err := newGatewayHandler(value, "http://127.0.0.1:1", "http://127.0.0.1:1"); err == nil {
			t.Errorf("expected invalid URL %q to be rejected", value)
		}
	}
}
