package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

var errPaymentNotFound = errors.New("payment not found")

type paymentCanceler interface {
	CancelPayment(context.Context, string) error
}

func (store postgresPaymentStore) CancelPayment(ctx context.Context, uid string) error {
	result, err := store.pool.Exec(ctx,
		"UPDATE payment SET status = 'CANCELED' WHERE payment_uid = $1", uid)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return errPaymentNotFound
	}
	return nil
}

func cancelPaymentHandler(store paymentCanceler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := r.PathValue("paymentUid")
		var parsed pgtype.UUID
		if err := parsed.Scan(uid); err != nil || !parsed.Valid {
			paymentError(w, http.StatusBadRequest, "paymentUid must be a valid UUID")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := store.CancelPayment(ctx, uid); err != nil {
			if errors.Is(err, errPaymentNotFound) {
				paymentError(w, http.StatusNotFound, "Payment not found")
			} else {
				log.Printf("cancel payment: %v", err)
				paymentError(w, http.StatusInternalServerError, "Unable to cancel payment")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
