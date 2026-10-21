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

type hotelReader interface {
	GetHotel(context.Context, string) (Hotel, error)
}

func (store postgresReservationStore) GetHotel(ctx context.Context, uid string) (Hotel, error) {
	var hotel Hotel
	err := store.pool.QueryRow(ctx, `
  SELECT hotel_uid::text, name, country, city, address, stars, price
  FROM hotels WHERE hotel_uid = $1`, uid).Scan(
		&hotel.HotelUID, &hotel.Name, &hotel.Country, &hotel.City,
		&hotel.Address, &hotel.Stars, &hotel.Price,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Hotel{}, errHotelNotFound
	}
	if err != nil {
		return Hotel{}, err
	}
	return hotel, nil
}

func getHotelHandler(store hotelReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := r.PathValue("hotelUid")
		var parsed pgtype.UUID
		if err := parsed.Scan(uid); err != nil || !parsed.Valid {
			writeJSONError(w, http.StatusBadRequest, "hotelUid must be a valid UUID")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		hotel, err := store.GetHotel(ctx, uid)
		if errors.Is(err, errHotelNotFound) {
			writeJSONError(w, http.StatusNotFound, "Hotel not found")
			return
		}
		if err != nil {
			log.Printf("Read hotel: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Unable to read hotel")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(hotel); err != nil {
			log.Printf("Write hotel response: %v", err)
		}
	}
}
