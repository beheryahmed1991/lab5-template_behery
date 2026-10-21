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
	"github.com/jackc/pgx/v5/pgxpool"
)

type Loyalty struct {
	Status           string `json:"status"`
	Discount         int    `json:"discount"`
	ReservationCount int    `json:"reservationCount"`
}

type loyaltyReader interface {
	GetLoyalty(context.Context, string) (Loyalty, error)
}

type postgresLoyaltyStore struct{ pool *pgxpool.Pool }

func (store postgresLoyaltyStore) GetLoyalty(ctx context.Context, username string) (Loyalty, error) {
	var loyalty Loyalty
	err := store.pool.QueryRow(ctx,
		"SELECT status, discount, reservation_count FROM loyalty WHERE username = $1", username).
		Scan(&loyalty.Status, &loyalty.Discount, &loyalty.ReservationCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Loyalty{Status: "BRONZE", Discount: 5, ReservationCount: 0}, nil
	}
	return loyalty, err
}

func getLoyaltyHandler(store loyaltyReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get("X-User-Name")
		if strings.TrimSpace(username) == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 80 {
			loyaltyError(w, http.StatusBadRequest, "X-User-Name is required and must contain at most 80 characters")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		loyalty, err := store.GetLoyalty(ctx, username)
		if err != nil {
			log.Printf("get loyalty: %v", err)
			loyaltyError(w, http.StatusInternalServerError, "Unable to load loyalty")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(loyalty); err != nil {
			log.Printf("write loyalty response: %v", err)
		}
	}
}

func loyaltyError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"message": message}); err != nil {
		log.Printf("write loyalty error: %v", err)
	}
}
