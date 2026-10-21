package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

type fakePaymentStore struct {
	price int32
	calls int
	err   error
}

func (store *fakePaymentStore) CreatePayment(ctx context.Context, price int32) (Payment, error) {
	store.calls++
	store.price = price
	if _, ok := ctx.Deadline(); !ok {
		return Payment{}, errors.New("database request has no timeout")
	}
	return Payment{PaymentUID: "049161bb-badd-4fa8-9d90-87c9a82b0668", Status: "PAID", Price: price}, store.err
}

func TestCreatePaymentSuccess(t *testing.T) {
	store := &fakePaymentStore{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(`{"price":27000}`))
	createPaymentHandler(store).ServeHTTP(response, request)
	if response.Code != http.StatusCreated || store.calls != 1 || store.price != 27000 {
		t.Fatalf("unexpected status or store call: status=%d calls=%d price=%d", response.Code, store.calls, store.price)
	}
	var payment Payment
	if err := json.Unmarshal(response.Body.Bytes(), &payment); err != nil {
		t.Fatal(err)
	}
	if payment.PaymentUID == "" || payment.Status != "PAID" || payment.Price != 27000 {
		t.Fatalf("unexpected payment response: %+v", payment)
	}
}

func TestCreatePaymentRejectsInvalidRequests(t *testing.T) {
	for _, body := range []string{
		``, `{`, `{}`, `null`, `{"price":null}`, `{"price":-1}`,
		`{"price":1.5}`, `{"price":"100"}`, `{"price":2147483648}`,
		`{"price":100,"status":"PAID"}`, `{"price":100}{"price":200}`,
	} {
		t.Run(body, func(t *testing.T) {
			store := &fakePaymentStore{}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(body))
			createPaymentHandler(store).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || store.calls != 0 {
				t.Fatalf("invalid request must be rejected before saving: status=%d calls=%d", response.Code, store.calls)
			}
		})
	}
}

func TestCreatePaymentDatabaseFailure(t *testing.T) {
	store := &fakePaymentStore{err: errors.New("private database details")}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(`{"price":100}`))
	createPaymentHandler(store).ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private database details") {
		t.Fatalf("unexpected database failure response: %d %s", response.Code, response.Body.String())
	}
}

func TestPaymentUIDIsRandomUUIDv4(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		uid, err := newPaymentUID()
		if err != nil || !pattern.MatchString(uid) || seen[uid] {
			t.Fatalf("invalid or repeated UUID: %q, %v", uid, err)
		}
		seen[uid] = true
	}
}
