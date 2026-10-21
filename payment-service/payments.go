package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Payment struct {
	PaymentUID string `json:"paymentUid"`
	Status     string `json:"status"`
	Price      int32  `json:"price"`
}

type paymentCreator interface {
	CreatePayment(context.Context, int32) (Payment, error)
}

type postgresPaymentStore struct {
	pool *pgxpool.Pool
}

func (store postgresPaymentStore) CreatePayment(ctx context.Context, price int32) (Payment, error) {
	uid, err := newPaymentUID()
	if err != nil {
		return Payment{}, err
	}
	var payment Payment
	err = store.pool.QueryRow(ctx, `
		INSERT INTO payment (payment_uid, status, price)
		VALUES ($1, 'PAID', $2)
		RETURNING payment_uid::text, status, price`, uid, price).
		Scan(&payment.PaymentUID, &payment.Status, &payment.Price)
	return payment, err
}

func newPaymentUID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate payment UUID: %w", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}

func createPaymentHandler(store paymentCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request struct {
			Price *int64 `json:"price"`
		}
		if err := decoder.Decode(&request); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				paymentError(w, http.StatusRequestEntityTooLarge, "Request body is too large")
			} else {
				paymentError(w, http.StatusBadRequest, "Expected a JSON object containing an integer price")
			}
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			paymentError(w, http.StatusBadRequest, "Request must contain a single JSON object")
			return
		}
		if request.Price == nil || *request.Price < 0 || *request.Price > 2147483647 {
			paymentError(w, http.StatusBadRequest, "price must be an integer between 0 and 2147483647")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		payment, err := store.CreatePayment(ctx, int32(*request.Price))
		if err != nil {
			log.Printf("create payment: %v", err)
			paymentError(w, http.StatusInternalServerError, "Unable to create payment")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(payment); err != nil {
			log.Printf("write payment response: %v", err)
		}
	}
}

func paymentError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"message": message}); err != nil {
		log.Printf("write payment error: %v", err)
	}
}
