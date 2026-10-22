package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type bookingClient struct {
	reservationURL, loyaltyURL, paymentURL string
	http                                   *http.Client
}

type bookingRequest struct {
	HotelUID  string `json:"hotelUid"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

type bookingPayment struct {
	UID    string `json:"paymentUid,omitempty"`
	Status string `json:"status"`
	Price  int64  `json:"price"`
}

type bookingResponse struct {
	ReservationUID string         `json:"reservationUid"`
	HotelUID       string         `json:"hotelUid"`
	StartDate      string         `json:"startDate"`
	EndDate        string         `json:"endDate"`
	Discount       int            `json:"discount"`
	Status         string         `json:"status"`
	Payment        bookingPayment `json:"payment"`
}

type serviceError struct{ status int }

func (err serviceError) Error() string { return fmt.Sprintf("service returned HTTP %d", err.status) }

func gatewayError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func validBookingUUID(uid string) bool {
	if len(uid) != 36 || uid[8] != '-' || uid[13] != '-' || uid[18] != '-' || uid[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(uid, "-", ""))
	return err == nil
}

func bookingNights(request bookingRequest) (int64, error) {
	if !validBookingUUID(request.HotelUID) {
		return 0, errors.New("hotelUid must be a valid UUID")
	}
	start, err := time.Parse("2006-01-02", request.StartDate)
	if err != nil {
		return 0, errors.New("startDate must use YYYY-MM-DD format")
	}
	end, err := time.Parse("2006-01-02", request.EndDate)
	if err != nil {
		return 0, errors.New("endDate must use YYYY-MM-DD format")
	}
	if !end.After(start) {
		return 0, errors.New("endDate must be after startDate")
	}
	// Unix arithmetic also covers ranges longer than time.Duration can represent.
	return (end.Unix() - start.Unix()) / 86400, nil
}

func bookingPrice(nights, nightly int64, discount int) (int64, error) {
	if nights <= 0 || nightly < 0 || discount < 0 || discount > 100 {
		return 0, errors.New("invalid pricing data")
	}
	const maxInt64 = int64(1<<63 - 1)
	if nightly > maxInt64/nights {
		return 0, errors.New("booking price is too large")
	}
	total := nights * nightly
	// Integer arithmetic rounds the discounted price down without multiplication overflow.
	factor := int64(100 - discount)
	price := (total/100)*factor + (total%100)*factor/100
	if price > 2147483647 {
		return 0, errors.New("booking price exceeds payment limit")
	}
	return price, nil
}

func (client bookingClient) call(ctx context.Context, method, base, path, username string, input, output any) error {
	return client.callWithKey(ctx, method, base, path, username, input, output, "")
}

func (client bookingClient) callWithKey(ctx context.Context, method, base, path, username string, input, output any, key string) error {
	endpoint, err := url.JoinPath(base, path)
	if err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}

	if authorization := identityFromContext(ctx).Authorization; authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response, err := client.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return serviceError{response.StatusCode}
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output)
	}
	return nil
}

func (client bookingClient) rollback(parent context.Context, username, reservationUID, paymentUID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 12*time.Second)
	defer cancel()
	if reservationUID != "" {
		if err := client.call(ctx, http.MethodDelete, client.reservationURL, "/api/v1/reservations/"+reservationUID, username, nil, nil); err != nil {
			log.Printf("Rollback reservation %s failed: %v", reservationUID, err)
		}
	}
	if paymentUID != "" {
		if err := client.call(ctx, http.MethodDelete, client.paymentURL, "/api/v1/payments/"+paymentUID, "", nil, nil); err != nil {
			log.Printf("Rollback payment %s failed: %v", paymentUID, err)
		}
	}
}

func (client bookingClient) createBooking(w http.ResponseWriter, r *http.Request) {
	username := identityFromContext(r.Context()).Subject
	if strings.TrimSpace(username) == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 80 {
		gatewayError(w, 400, "JWT subject must contain between 1 and 80 characters")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request bookingRequest
	if err := decoder.Decode(&request); err != nil {
		gatewayError(w, 400, "Expected hotelUid, startDate and endDate in a JSON object")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		gatewayError(w, 400, "Request must contain a single JSON object")
		return
	}
	nights, err := bookingNights(request)
	if err != nil {
		gatewayError(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var hotel struct {
		Price int64 `json:"price"`
	}
	if err := client.call(ctx, http.MethodGet, client.reservationURL, "/api/v1/hotels/"+request.HotelUID, "", nil, &hotel); err != nil {
		var failure serviceError
		if errors.As(err, &failure) && failure.status == 404 {
			gatewayError(w, 404, "Hotel not found")
		} else {
			gatewayError(w, 503, "Reservation Service unavailable")
		}
		return
	}
	var loyalty struct {
		Discount int `json:"discount"`
	}
	if err := client.call(ctx, http.MethodGet, client.loyaltyURL, "/api/v1/loyalty", username, nil, &loyalty); err != nil {
		gatewayError(w, 503, "Loyalty Service unavailable")
		return
	}
	price, err := bookingPrice(nights, hotel.Price, loyalty.Discount)
	if err != nil {
		gatewayError(w, 400, err.Error())
		return
	}
	var payment bookingPayment
	if err := client.call(ctx, http.MethodPost, client.paymentURL, "/api/v1/payments", "", map[string]int64{"price": price}, &payment); err != nil {
		gatewayError(w, 503, "Payment Service unavailable")
		return
	}
	if !validBookingUUID(payment.UID) {
		gatewayError(w, 503, "Invalid Payment Service response")
		return
	}
	var reservation struct {
		UID    string `json:"reservationUid"`
		Status string `json:"status"`
	}
	internal := map[string]string{"hotelUid": request.HotelUID, "paymentUid": payment.UID, "startDate": request.StartDate, "endDate": request.EndDate}
	if err := client.call(ctx, http.MethodPost, client.reservationURL, "/api/v1/reservations", username, internal, &reservation); err != nil {
		client.rollback(ctx, username, "", payment.UID)
		gatewayError(w, 503, "Unable to create reservation")
		return
	}
	if !validBookingUUID(reservation.UID) {
		client.rollback(ctx, username, "", payment.UID)
		gatewayError(w, 503, "Invalid Reservation Service response")
		return
	}
	if err := client.call(ctx, http.MethodPatch, client.loyaltyURL, "/api/v1/loyalty", username, map[string]int{"reservationCountChange": 1}, nil); err != nil {
		client.rollback(ctx, username, reservation.UID, payment.UID)
		gatewayError(w, 503, "Unable to update loyalty")
		return
	}
	payment.UID = ""
	result := bookingResponse{reservation.UID, request.HotelUID, request.StartDate, request.EndDate, loyalty.Discount, reservation.Status, payment}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Location", "/api/v1/reservations/"+reservation.UID)
	// The instructor's public API specifies HTTP 200 for a successful booking.
	json.NewEncoder(w).Encode(result)
}
