package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type reservationLister interface {
	ListReservations(context.Context, string) ([]Reservation, error)
}

func (store postgresReservationStore) ListReservations(ctx context.Context, username string) ([]Reservation, error) {
	rows, err := store.pool.Query(ctx, `
  SELECT r.reservation_uid::text, r.username, h.hotel_uid::text,
         r.payment_uid::text, r.status, r.start_date, r.end_data
  FROM reservation r JOIN hotels h ON h.id = r.hotel_id
  WHERE r.username = $1 ORDER BY r.id`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reservations := make([]Reservation, 0)
	for rows.Next() {
		var reservation Reservation
		var start, end time.Time
		if err := rows.Scan(&reservation.ReservationUID, &reservation.Username,
			&reservation.HotelUID, &reservation.PaymentUID, &reservation.Status, &start, &end); err != nil {
			return nil, err
		}
		reservation.StartDate = start.UTC().Format("2006-01-02")
		reservation.EndDate = end.UTC().Format("2006-01-02")
		reservations = append(reservations, reservation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return reservations, nil
}

func listReservationsHandler(store reservationLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get("X-User-Name")
		if strings.TrimSpace(username) == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 80 {
			writeJSONError(w, http.StatusBadRequest, "X-User-Name must contain between 1 and 80 characters")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		reservations, err := store.ListReservations(ctx, username)
		if err != nil {
			log.Printf("List reservations: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Unable to list reservations")
			return
		}
		if reservations == nil {
			reservations = make([]Reservation, 0)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(reservations)
	}
}
