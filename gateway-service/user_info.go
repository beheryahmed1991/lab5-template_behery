package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type loyaltyInfo struct {
	Status           string `json:"status"`
	Discount         int    `json:"discount"`
	ReservationCount int    `json:"reservationCount"`
}

type userInfo struct {
	Reservations []reservationInfo `json:"reservations"`
	Loyalty      loyaltyInfo       `json:"loyalty"`
}

func (client bookingClient) getUserInfo(w http.ResponseWriter, r *http.Request) {
	username, ok := gatewayUsername(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	reservations, err := client.reservationList(ctx, username)
	if err != nil {
		log.Printf("Read user reservations: %v", err)
		gatewayError(w, http.StatusServiceUnavailable, "Unable to load reservations")
		return
	}
	var loyalty loyaltyInfo
	if err := client.call(ctx, http.MethodGet, client.loyaltyURL, "/api/v1/loyalty", username, nil, &loyalty); err != nil {
		log.Printf("Read user loyalty: %v", err)
		loyalty = loyaltyInfo{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(userInfo{reservations, loyalty}); err != nil {
		log.Printf("Write user information: %v", err)
	}
}
