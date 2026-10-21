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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errHotelNotFound = errors.New("hotel not found")

type Reservation struct {
	ReservationUID string `json:"reservationUid"`
	Username       string `json:"username"`
	HotelUID       string `json:"hotelUid"`
	PaymentUID     string `json:"paymentUid"`
	Status         string `json:"status"`
	StartDate      string `json:"startDate"`
	EndDate        string `json:"endDate"`
}

type createReservationRequest struct {
	HotelUID   string `json:"hotelUid"`
	PaymentUID string `json:"paymentUid"`
	StartDate  string `json:"startDate"`
	EndDate    string `json:"endDate"`
	start      time.Time
	end        time.Time
}

type reservationCreator interface {
	CreateReservation(context.Context, string, createReservationRequest) (Reservation, error)
}

type postgresReservationStore struct{ pool *pgxpool.Pool }

func validateReservationRequest(request *createReservationRequest) error {
	for field, value := range map[string]string{"hotelUid": request.HotelUID, "paymentUid": request.PaymentUID} {
		var uid pgtype.UUID
		if err := uid.Scan(value); err != nil || !uid.Valid {
			return fmt.Errorf("%s must be a valid UUID", field)
		}
	}
	var err error
	request.start, err = time.Parse("2006-01-02", request.StartDate)
	if err != nil {
		return fmt.Errorf("startDate must be a valid date in YYYY-MM-DD format")
	}
	request.end, err = time.Parse("2006-01-02", request.EndDate)
	if err != nil {
		return fmt.Errorf("endDate must be a valid date in YYYY-MM-DD format")
	}
	if !request.end.After(request.start) {
		return fmt.Errorf("endDate must be after startDate")
	}
	return nil
}

func newReservationUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
}

func (store postgresReservationStore) CreateReservation(ctx context.Context, username string, request createReservationRequest) (Reservation, error) {
	uid, err := newReservationUID()
	if err != nil {
		return Reservation{}, err
	}
	result := Reservation{Username: username, HotelUID: request.HotelUID, PaymentUID: request.PaymentUID, StartDate: request.StartDate, EndDate: request.EndDate}
	err = store.pool.QueryRow(ctx, `
  INSERT INTO reservation (reservation_uid, username, payment_uid, hotel_id, status, start_date, end_data)
  SELECT $1, $2, $3, id, 'PAID', $5, $6 FROM hotels WHERE hotel_uid=$4
  RETURNING reservation_uid::text, status`, uid, username, request.PaymentUID, request.HotelUID, request.start, request.end).
		Scan(&result.ReservationUID, &result.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, errHotelNotFound
	}
	return result, err
}

func createReservationHandler(store reservationCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get("X-User-Name")
		if strings.TrimSpace(username) == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 80 {
			writeJSONError(w, 400, "X-User-Name is required and must contain at most 80 characters")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request createReservationRequest
		if err := decoder.Decode(&request); err != nil {
			writeJSONError(w, 400, "Expected hotelUid, paymentUid, startDate and endDate in a JSON object")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeJSONError(w, 400, "Request must contain a single JSON object")
			return
		}
		if err := validateReservationRequest(&request); err != nil {
			writeJSONError(w, 400, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := store.CreateReservation(ctx, username, request)
		if err != nil {
			if errors.Is(err, errHotelNotFound) {
				writeJSONError(w, 404, "Hotel not found")
			} else {
				log.Printf("create reservation: %v", err)
				writeJSONError(w, 500, "Unable to create reservation")
			}
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Location", "/api/v1/reservations/"+result.ReservationUID)
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(result); err != nil {
			log.Printf("write reservation response: %v", err)
		}
	}
}
