package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var errReservationNotFound = errors.New("reservation not found")

type reservationReader interface {
	GetReservation(context.Context, string, string) (Reservation, error)
}

func (store postgresReservationStore) GetReservation(ctx context.Context, username, uid string) (Reservation, error) {
	var reservation Reservation
	var start, end time.Time
	err := store.pool.QueryRow(ctx, `
  SELECT r.reservation_uid::text, r.username, h.hotel_uid::text,
         r.payment_uid::text, r.status, r.start_date, r.end_data
  FROM reservation r JOIN hotels h ON h.id = r.hotel_id
  WHERE r.reservation_uid = $1 AND r.username = $2`, uid, username).Scan(
		&reservation.ReservationUID, &reservation.Username, &reservation.HotelUID,
		&reservation.PaymentUID, &reservation.Status, &start, &end,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, errReservationNotFound
	}
	if err != nil {
		return Reservation{}, err
	}
	reservation.StartDate = start.UTC().Format("2006-01-02")
	reservation.EndDate = end.UTC().Format("2006-01-02")
	return reservation, nil
}

func getReservationHandler(store reservationReader) http.HandlerFunc {
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
		reservation, err := store.GetReservation(ctx, username, uid)
		if errors.Is(err, errReservationNotFound) {
			writeJSONError(w, http.StatusNotFound, "Reservation not found")
			return
		}
		if err != nil {
			log.Printf("Read reservation: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Unable to read reservation")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(reservation)
	}
}
