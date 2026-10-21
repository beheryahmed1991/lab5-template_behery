package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePaymentCanceler struct {
	uid   string
	calls int
	err   error
}

func (store *fakePaymentCanceler) CancelPayment(ctx context.Context, uid string) error {
	store.uid = uid
	store.calls++
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("missing cancellation timeout")
	}
	return store.err
}

func TestCancelPaymentResponses(t *testing.T) {
	const uid = "08fb7dad-63a3-40b3-9f2e-10dc1ea0985e"
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{"existing payment", nil, http.StatusNoContent},
		{"missing payment", errPaymentNotFound, http.StatusNotFound},
		{"database failure", errors.New("private database details"), http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakePaymentCanceler{err: test.err}
			mux := http.NewServeMux()
			mux.HandleFunc("DELETE /api/v1/payments/{paymentUid}", cancelPaymentHandler(store))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/payments/"+uid, nil))
			if response.Code != test.status || store.calls != 1 || store.uid != uid {
				t.Fatalf("unexpected cancellation: status=%d calls=%d uid=%q", response.Code, store.calls, store.uid)
			}
			if test.status == http.StatusNoContent && response.Body.Len() != 0 {
				t.Fatal("204 response must have no body")
			}
			if strings.Contains(response.Body.String(), "private database details") {
				t.Fatal("database details must not be returned to the caller")
			}
		})
	}
}

func TestCancelPaymentRejectsInvalidUID(t *testing.T) {
	for _, uid := range []string{"not-a-uuid", "08fb7dad-63a3-40b3-9f2e-10dc1ea0985z"} {
		store := &fakePaymentCanceler{}
		mux := http.NewServeMux()
		mux.HandleFunc("DELETE /api/v1/payments/{paymentUid}", cancelPaymentHandler(store))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/payments/"+uid, nil))
		if response.Code != http.StatusBadRequest || store.calls != 0 {
			t.Fatalf("invalid UUID reached the database: status=%d calls=%d", response.Code, store.calls)
		}
	}
}
