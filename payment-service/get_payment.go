package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type paymentReader interface {
	GetPayment(context.Context, string) (Payment, error)
}

func (store postgresPaymentStore) GetPayment(ctx context.Context, uid string) (Payment, error) {
	var payment Payment
	err := store.pool.QueryRow(ctx,
		"SELECT payment_uid::text, status, price FROM payment WHERE payment_uid = $1", uid).
		Scan(&payment.PaymentUID, &payment.Status, &payment.Price)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, errPaymentNotFound
	}
	return payment, err
}

func getPaymentHandler(store paymentReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := r.PathValue("paymentUid")
		var parsed pgtype.UUID
		if err := parsed.Scan(uid); err != nil || !parsed.Valid {
			paymentError(w, http.StatusBadRequest, "paymentUid must be a valid UUID")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		payment, err := store.GetPayment(ctx, uid)
		if err != nil {
			if errors.Is(err, errPaymentNotFound) {
				paymentError(w, http.StatusNotFound, "Payment not found")
			} else {
				log.Printf("get payment: %v", err)
				paymentError(w, http.StatusInternalServerError, "Unable to load payment")
			}
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(payment); err != nil {
			log.Printf("write payment response: %v", err)
		}
	}
}
