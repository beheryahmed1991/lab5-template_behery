package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

type reservationCanceler interface {
	CancelReservation(context.Context, string, string) error
}

func (store postgresReservationStore) CancelReservation(ctx context.Context, username, uid string) error {
	result, err := store.pool.Exec(ctx,
		"UPDATE reservation SET status = 'CANCELED' WHERE reservation_uid = $1 AND username = $2", uid, username)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return errReservationNotFound
	}
	return nil
}

func cancelReservationHandler(store reservationCanceler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get("X-User-Name")
		if strings.TrimSpace(username) == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 80 {
			writeJSONError(w, http.StatusBadRequest, "X-User-Name must contain between 1 and 80 characters")
			return
		}
		uid := r.PathValue("reservationUid")
		var parsed pgtype.UUID
		if err := parsed.Scan(uid); err != nil || !parsed.Valid {
			writeJSONError(w, http.StatusBadRequest, "reservationUid must be a valid UUID")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := store.CancelReservation(ctx, username, uid); err != nil {
			if errors.Is(err, errReservationNotFound) {
				writeJSONError(w, http.StatusNotFound, "Reservation not found")
			} else {
				log.Printf("Cancel reservation: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "Unable to cancel reservation")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
