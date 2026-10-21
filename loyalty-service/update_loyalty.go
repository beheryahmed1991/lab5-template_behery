package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type loyaltyUpdater interface {
	UpdateLoyalty(context.Context, string, int) (Loyalty, error)
}

type idempotentLoyaltyUpdater interface {
	UpdateLoyaltyOnce(context.Context, string, int, string) (Loyalty, error)
}

var errOperationConflict = errors.New("idempotency key was already used for another update")

func loyaltyForCount(count int) Loyalty {
	if count < 0 {
		count = 0
	}
	result := Loyalty{Status: "BRONZE", Discount: 5, ReservationCount: count}
	if count >= 20 {
		result.Status = "GOLD"
		result.Discount = 10
	} else if count >= 10 {
		result.Status = "SILVER"
		result.Discount = 7
	}
	return result
}

func (store postgresLoyaltyStore) UpdateLoyalty(ctx context.Context, username string, change int) (Loyalty, error) {
	return store.updateLoyalty(ctx, username, change, "")
}

func (store postgresLoyaltyStore) UpdateLoyaltyOnce(ctx context.Context, username string, change int, key string) (Loyalty, error) {
	return store.updateLoyalty(ctx, username, change, key)
}

func (store postgresLoyaltyStore) updateLoyalty(ctx context.Context, username string, change int, key string) (Loyalty, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return Loyalty{}, err
	}
	defer tx.Rollback(context.Background())
	if key != "" {
		// The operation record and counter change commit atomically. A concurrent
		// duplicate INSERT waits for the first transaction before reading its record.
		tag, err := tx.Exec(ctx, `INSERT INTO loyalty_updates (operation_key, username, change)
            VALUES ($1, $2, $3) ON CONFLICT (operation_key) DO NOTHING`, key, username, change)
		if err != nil {
			return Loyalty{}, err
		}
		if tag.RowsAffected() == 0 {
			var previousUser string
			var previousChange int
			if err := tx.QueryRow(ctx, "SELECT username, change FROM loyalty_updates WHERE operation_key = $1", key).Scan(&previousUser, &previousChange); err != nil {
				return Loyalty{}, err
			}
			if previousUser != username || previousChange != change {
				return Loyalty{}, errOperationConflict
			}
			var result Loyalty
			err := tx.QueryRow(ctx, "SELECT status, discount, reservation_count FROM loyalty WHERE username = $1", username).Scan(&result.Status, &result.Discount, &result.ReservationCount)
			if errors.Is(err, pgx.ErrNoRows) {
				return Loyalty{}, errors.New("loyalty operation has no matching user")
			}
			return result, err
		}
	}
	var count int
	// The upsert locks this user's row until commit, preserving concurrent updates.
	err = tx.QueryRow(ctx, `
  INSERT INTO loyalty (username, reservation_count, status, discount)
  VALUES ($1, GREATEST($2::integer, 0), 'BRONZE', 5)
  ON CONFLICT (username) DO UPDATE
  SET reservation_count = GREATEST(loyalty.reservation_count + $2::integer, 0)
  RETURNING reservation_count`, username, change).Scan(&count)
	if err != nil {
		return Loyalty{}, err
	}
	result := loyaltyForCount(count)
	if _, err := tx.Exec(ctx,
		"UPDATE loyalty SET status = $2, discount = $3 WHERE username = $1",
		username, result.Status, result.Discount); err != nil {
		return Loyalty{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Loyalty{}, err
	}
	return result, nil
}

func updateLoyaltyHandler(store loyaltyUpdater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get("X-User-Name")
		if strings.TrimSpace(username) == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 80 {
			loyaltyError(w, 400, "X-User-Name is required and must contain at most 80 characters")
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if len(key) > 128 || strings.TrimSpace(key) != key {
			loyaltyError(w, 400, "Invalid Idempotency-Key")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request struct {
			Change *int `json:"reservationCountChange"`
		}
		if err := decoder.Decode(&request); err != nil {
			loyaltyError(w, 400, "Expected a JSON object containing reservationCountChange")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			loyaltyError(w, 400, "Request must contain a single JSON object")
			return
		}
		if request.Change == nil || (*request.Change != 1 && *request.Change != -1) {
			loyaltyError(w, 400, "reservationCountChange must be 1 or -1")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var result Loyalty
		var err error
		if key == "" {
			result, err = store.UpdateLoyalty(ctx, username, *request.Change)
		} else if once, ok := store.(idempotentLoyaltyUpdater); ok {
			result, err = once.UpdateLoyaltyOnce(ctx, username, *request.Change, key)
		} else {
			err = errors.New("store does not support idempotent updates")
		}
		if errors.Is(err, errOperationConflict) {
			loyaltyError(w, 409, "Idempotency-Key already used for another update")
			return
		}
		if err != nil {
			log.Printf("update loyalty: %v", err)
			loyaltyError(w, 500, "Unable to update loyalty")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			log.Printf("write updated loyalty: %v", err)
		}
	}
}
