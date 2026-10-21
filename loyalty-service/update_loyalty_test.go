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

func TestLoyaltyTierBoundaries(t *testing.T) {
	for _, tt := range []struct {
		count    int
		status   string
		discount int
	}{
		{0, "BRONZE", 5}, {9, "BRONZE", 5}, {10, "SILVER", 7}, {19, "SILVER", 7}, {20, "GOLD", 10}, {25, "GOLD", 10},
	} {
		got := loyaltyForCount(tt.count)
		if got.Status != tt.status || got.Discount != tt.discount || got.ReservationCount != tt.count {
			t.Errorf("count %d: got %+v", tt.count, got)
		}
	}
	if got := loyaltyForCount(-1); got.ReservationCount != 0 {
		t.Fatal("count must not be negative")
	}
}

type fakeLoyaltyUpdater struct {
	username string
	change   int
	calls    int
	err      error
}

func (store *fakeLoyaltyUpdater) UpdateLoyalty(ctx context.Context, username string, change int) (Loyalty, error) {
	store.username = username
	store.change = change
	store.calls++
	if _, ok := ctx.Deadline(); !ok {
		return Loyalty{}, errors.New("missing timeout")
	}
	return loyaltyForCount(25 + change), store.err
}

func TestUpdateLoyaltyBothDirections(t *testing.T) {
	for _, tt := range []struct {
		body   string
		change int
	}{{`{"reservationCountChange":1}`, 1}, {`{"reservationCountChange":-1}`, -1}} {
		store := &fakeLoyaltyUpdater{}
		request := httptest.NewRequest(http.MethodPatch, "/api/v1/loyalty", strings.NewReader(tt.body))
		request.Header.Set("X-User-Name", "Test Max")
		response := httptest.NewRecorder()
		updateLoyaltyHandler(store).ServeHTTP(response, request)
		var got Loyalty
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || store.calls != 1 || store.change != tt.change || store.username != "Test Max" || got != loyaltyForCount(25+tt.change) {
			t.Fatalf("unexpected update: status=%d result=%+v store=%+v", response.Code, got, store)
		}
	}
}

func TestUpdateLoyaltyRejectsInvalidChanges(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"reservationCountChange":null}`, `{"reservationCountChange":0}`, `{"reservationCountChange":2}`, `{"reservationCountChange":1.5}`, `{"reservationCountChange":"1"}`, `{"reservationCountChange":1,"extra":true}`, `{"reservationCountChange":1}{}`} {
		store := &fakeLoyaltyUpdater{}
		request := httptest.NewRequest(http.MethodPatch, "/api/v1/loyalty", strings.NewReader(body))
		request.Header.Set("X-User-Name", "Test Max")
		response := httptest.NewRecorder()
		updateLoyaltyHandler(store).ServeHTTP(response, request)
		if response.Code != 400 || store.calls != 0 {
			t.Fatalf("invalid request reached store: %q, status=%d calls=%d", body, response.Code, store.calls)
		}
	}
}

func TestUpdateLoyaltyMissingUsername(t *testing.T) {
	store := &fakeLoyaltyUpdater{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/loyalty", strings.NewReader(`{"reservationCountChange":1}`))
	response := httptest.NewRecorder()
	updateLoyaltyHandler(store).ServeHTTP(response, request)
	if response.Code != 400 || store.calls != 0 {
		t.Fatal("missing username must be rejected before update")
	}
}

func TestUpdateLoyaltyDatabaseFailure(t *testing.T) {
	store := &fakeLoyaltyUpdater{err: errors.New("private database details")}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/loyalty", strings.NewReader(`{"reservationCountChange":1}`))
	request.Header.Set("X-User-Name", "Test Max")
	response := httptest.NewRecorder()
	updateLoyaltyHandler(store).ServeHTTP(response, request)
	if response.Code != 500 || strings.Contains(response.Body.String(), "private database details") {
		t.Fatalf("unexpected failure response: %d %s", response.Code, response.Body.String())
	}
}
