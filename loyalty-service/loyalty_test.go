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

type fakeLoyaltyReader struct {
	result   Loyalty
	err      error
	username string
	calls    int
}

func (store *fakeLoyaltyReader) GetLoyalty(ctx context.Context, username string) (Loyalty, error) {
	store.username = username
	store.calls++
	if _, ok := ctx.Deadline(); !ok {
		return Loyalty{}, errors.New("missing timeout")
	}
	return store.result, store.err
}
func TestGetLoyaltyResponse(t *testing.T) {
	for _, expected := range []Loyalty{{Status: "GOLD", Discount: 10, ReservationCount: 25}, {Status: "BRONZE", Discount: 5, ReservationCount: 0}} {
		store := &fakeLoyaltyReader{result: expected}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/loyalty", nil)
		request.Header.Set("X-User-Name", "Test Max")
		response := httptest.NewRecorder()
		getLoyaltyHandler(store).ServeHTTP(response, request)
		var got Loyalty
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || got != expected || store.username != "Test Max" {
			t.Fatalf("unexpected result: status=%d loyalty=%+v username=%q", response.Code, got, store.username)
		}
	}
}
func TestGetLoyaltyRejectsInvalidUsername(t *testing.T) {
	for _, username := range []string{"", "   ", strings.Repeat("x", 81)} {
		store := &fakeLoyaltyReader{}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/loyalty", nil)
		request.Header.Set("X-User-Name", username)
		response := httptest.NewRecorder()
		getLoyaltyHandler(store).ServeHTTP(response, request)
		if response.Code != 400 || store.calls != 0 {
			t.Fatalf("invalid username reached database: status=%d calls=%d", response.Code, store.calls)
		}
	}
}
func TestGetLoyaltyDatabaseFailure(t *testing.T) {
	store := &fakeLoyaltyReader{err: errors.New("private database details")}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/loyalty", nil)
	request.Header.Set("X-User-Name", "Test Max")
	response := httptest.NewRecorder()
	getLoyaltyHandler(store).ServeHTTP(response, request)
	if response.Code != 500 || strings.Contains(response.Body.String(), "private database details") {
		t.Fatalf("unexpected failure response: %d %s", response.Code, response.Body.String())
	}
}
