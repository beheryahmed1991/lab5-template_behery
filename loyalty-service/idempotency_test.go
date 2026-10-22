package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeOnceUpdater struct {
	fakeLoyaltyUpdater
	key string
	err error
}

func (store *fakeOnceUpdater) UpdateLoyaltyOnce(ctx context.Context, username string, change int, key string) (Loyalty, error) {
	store.key = key
	result, err := store.fakeLoyaltyUpdater.UpdateLoyalty(ctx, username, change)
	if store.err != nil {
		return Loyalty{}, store.err
	}
	return result, err
}

func TestLoyaltyIdempotencyHeader(t *testing.T) {
	for _, tc := range []struct {
		name, key     string
		failure       error
		status, calls int
	}{
		{"key forwarded", "cancel:abc", nil, 200, 1},
		{"key conflict", "cancel:abc", errOperationConflict, 409, 1},
		{"database failure", "cancel:abc", errors.New("database private error"), 500, 1},
		{"long key", strings.Repeat("a", 129), nil, 400, 0},
		{"whitespace key", " ", nil, 400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeOnceUpdater{err: tc.failure}
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/loyalty", strings.NewReader(`{"reservationCountChange":-1}`))
			request = testIdentityRequest(request, "Test Max")
			request.Header.Set("Idempotency-Key", tc.key)
			response := httptest.NewRecorder()
			updateLoyaltyHandler(store).ServeHTTP(response, request)
			if response.Code != tc.status || store.calls != tc.calls {
				t.Fatalf("status=%d calls=%d", response.Code, store.calls)
			}
			if tc.calls != 0 && (store.key != tc.key || store.change != -1 || store.username != "Test Max") {
				t.Fatal("operation details not forwarded")
			}
			if strings.Contains(response.Body.String(), "private") {
				t.Fatal("private error leaked")
			}
		})
	}
}

// Run against a migrated test database; ordinary unit runs need no PostgreSQL.
func TestPostgresLoyaltyIdempotency(t *testing.T) {
	databaseURL := os.Getenv("LOYALTY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set LOYALTY_TEST_DATABASE_URL for the PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := postgresLoyaltyStore{pool: pool}
	user := fmt.Sprintf("retry-test-%d", time.Now().UnixNano())
	key := "cancel:" + user
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		// Integration test uses an isolated user and cleans up only its own records.
		if _, err := pool.Exec(cleanup, "DELETE FROM loyalty_updates WHERE username=$1", user); err != nil {
			t.Error(err)
		}
		if _, err := pool.Exec(cleanup, "DELETE FROM loyalty WHERE username=$1", user); err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 3; i++ {
		if _, err := store.UpdateLoyalty(ctx, user, 1); err != nil {
			t.Fatal(err)
		}
	}
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := store.UpdateLoyaltyOnce(ctx, user, -1, key)
			if err != nil {
				t.Error(err)
				return
			}
			if result.ReservationCount != 2 {
				t.Errorf("count=%d, want 2", result.ReservationCount)
			}
		}()
	}
	workers.Wait()
	if _, err := store.UpdateLoyaltyOnce(ctx, user, 1, key); !errors.Is(err, errOperationConflict) {
		t.Fatalf("changed delta accepted: %v", err)
	}
	if _, err := store.UpdateLoyaltyOnce(ctx, user+"-other", -1, key); !errors.Is(err, errOperationConflict) {
		t.Fatalf("changed user accepted: %v", err)
	}
	// A new store instance still recognizes an already committed operation.
	fresh := postgresLoyaltyStore{pool: pool}
	got, err := fresh.UpdateLoyaltyOnce(ctx, user, -1, key)
	if err != nil || got.ReservationCount != 2 {
		t.Fatalf("repeat count=%d error=%v", got.ReservationCount, err)
	}
	var receipts int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM loyalty_updates WHERE operation_key=$1", key).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipts=%d error=%v", receipts, err)
	}
}
