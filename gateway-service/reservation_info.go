package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type internalReservation struct {
	ReservationUID string `json:"reservationUid"`
	Username       string `json:"username"`
	HotelUID       string `json:"hotelUid"`
	PaymentUID     string `json:"paymentUid"`
	Status         string `json:"status"`
	StartDate      string `json:"startDate"`
	EndDate        string `json:"endDate"`
}

type hotelInfo struct {
	HotelUID    string `json:"hotelUid"`
	Name        string `json:"name"`
	FullAddress string `json:"fullAddress"`
	Stars       *int   `json:"stars"`
}

type reservationInfo struct {
	ReservationUID string         `json:"reservationUid"`
	Hotel          hotelInfo      `json:"hotel"`
	StartDate      string         `json:"startDate"`
	EndDate        string         `json:"endDate"`
	Status         string         `json:"status"`
	Payment        bookingPayment `json:"payment"`
}

func gatewayUsername(w http.ResponseWriter, r *http.Request) (string, bool) {
	username := r.Header.Get("X-User-Name")
	if strings.TrimSpace(username) == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 80 {
		gatewayError(w, 400, "X-User-Name is required and must contain at most 80 characters")
		return "", false
	}
	return username, true
}

func (client bookingClient) enrichReservation(ctx context.Context, reservation internalReservation, hotels map[string]hotelInfo) (reservationInfo, error) {
	if !validBookingUUID(reservation.HotelUID) || !validBookingUUID(reservation.PaymentUID) || !validBookingUUID(reservation.ReservationUID) {
		return reservationInfo{}, fmt.Errorf("invalid reservation identifiers")
	}
	hotel, found := hotels[reservation.HotelUID]
	if !found {
		var source struct {
			HotelUID string `json:"hotelUid"`
			Name     string `json:"name"`
			Country  string `json:"country"`
			City     string `json:"city"`
			Address  string `json:"address"`
			Stars    *int   `json:"stars"`
		}
		if err := client.call(ctx, http.MethodGet, client.reservationURL, "/api/v1/hotels/"+reservation.HotelUID, "", nil, &source); err != nil {
			return reservationInfo{}, fmt.Errorf("load hotel: %w", err)
		}
		hotel = hotelInfo{source.HotelUID, source.Name, strings.Join([]string{source.Country, source.City, source.Address}, ", "), source.Stars}
		hotels[reservation.HotelUID] = hotel
	}
	var payment bookingPayment
	if err := client.call(ctx, http.MethodGet, client.paymentURL, "/api/v1/payments/"+reservation.PaymentUID, "", nil, &payment); err != nil {
		log.Printf("Payment fallback for reservation %s: %v", reservation.ReservationUID, err)
		payment = bookingPayment{}
	}
	payment.UID = ""
	return reservationInfo{reservation.ReservationUID, hotel, reservation.StartDate, reservation.EndDate, reservation.Status, payment}, nil
}

func (client bookingClient) reservationList(ctx context.Context, username string) ([]reservationInfo, error) {
	var source []internalReservation
	if err := client.call(ctx, http.MethodGet, client.reservationURL, "/api/v1/reservations", username, nil, &source); err != nil {
		return nil, err
	}
	result := make([]reservationInfo, 0, len(source))
	hotels := make(map[string]hotelInfo)
	for _, reservation := range source {
		if reservation.Username != username {
			return nil, errors.New("Reservation Service returned another user's reservation")
		}
		item, err := client.enrichReservation(ctx, reservation, hotels)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (client bookingClient) listBookingInfo(w http.ResponseWriter, r *http.Request) {
	username, ok := gatewayUsername(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := client.reservationList(ctx, username)
	if err != nil {
		log.Printf("Read reservation list: %v", err)
		gatewayError(w, 503, "Unable to load reservations")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(result)
}

func (client bookingClient) getBookingInfo(w http.ResponseWriter, r *http.Request) {
	username, ok := gatewayUsername(w, r)
	if !ok {
		return
	}
	uid := strings.ToLower(r.PathValue("reservationUid"))
	if !validBookingUUID(uid) {
		gatewayError(w, 400, "reservationUid must be a valid UUID")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var source internalReservation
	if err := client.call(ctx, http.MethodGet, client.reservationURL, "/api/v1/reservations/"+uid, username, nil, &source); err != nil {
		var failure serviceError
		if errors.As(err, &failure) && failure.status == 404 {
			gatewayError(w, 404, "Reservation not found")
		} else {
			gatewayError(w, 503, "Reservation Service unavailable")
		}
		return
	}
	if source.Username != username {
		gatewayError(w, 404, "Reservation not found")
		return
	}
	result, err := client.enrichReservation(ctx, source, make(map[string]hotelInfo))
	if err != nil {
		log.Printf("Read reservation details: %v", err)
		gatewayError(w, 503, "Unable to load reservation details")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(result)
}
