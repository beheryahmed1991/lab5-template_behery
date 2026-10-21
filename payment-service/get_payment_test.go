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

type fakePaymentReader struct {
	payment Payment
	err     error
	calls   int
	uid     string
}

func (store *fakePaymentReader) GetPayment(ctx context.Context, uid string) (Payment, error) {
	store.calls++
	store.uid = uid
	if _, ok := ctx.Deadline(); !ok {
		return Payment{}, errors.New("missing timeout")
	}
	return store.payment, store.err
}
func TestGetPaymentResponses(t *testing.T) {
	const uid = "08fb7dad-63a3-40b3-9f2e-10dc1ea0985e"
	tests := []struct {
		name   string
		status string
		err    error
		code   int
	}{
		{"paid", "PAID", nil, 200},
		{"canceled remains readable", "CANCELED", nil, 200},
		{"missing", "", errPaymentNotFound, 404},
		{"database error", "", errors.New("private database details"), 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakePaymentReader{payment: Payment{PaymentUID: uid, Status: tt.status, Price: 27000}, err: tt.err}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/payments/{paymentUid}", getPaymentHandler(store))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/payments/"+uid, nil))
			if response.Code != tt.code || store.uid != uid || store.calls != 1 {
				t.Fatalf("unexpected response: %d, calls=%d, uid=%s", response.Code, store.calls, store.uid)
			}
			if strings.Contains(response.Body.String(), "private database details") {
				t.Fatal("database details leaked")
			}
			if tt.code == 200 {
				var payment Payment
				if err := json.Unmarshal(response.Body.Bytes(), &payment); err != nil {
					t.Fatal(err)
				}
				if payment != store.payment {
					t.Fatalf("unexpected payment: %+v", payment)
				}
			}
		})
	}
}
func TestGetPaymentInvalidUID(t *testing.T) {
	store := &fakePaymentReader{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/payments/{paymentUid}", getPaymentHandler(store))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/payments/invalid", nil))
	if response.Code != 400 || store.calls != 0 {
		t.Fatalf("invalid UID reached database: %d, calls=%d", response.Code, store.calls)
	}
}
