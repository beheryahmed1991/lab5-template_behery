package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required; run the service with Docker Compose")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("configure database connection: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("connect to payments database: %w", err)
	}
	log.Println("Connected to payments database")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/payments/{paymentUid}", getPaymentHandler(postgresPaymentStore{pool: pool}))
	mux.HandleFunc("DELETE /api/v1/payments/{paymentUid}", cancelPaymentHandler(postgresPaymentStore{pool: pool}))
	mux.HandleFunc("POST /api/v1/payments", createPaymentHandler(postgresPaymentStore{pool: pool}))
	mux.HandleFunc("GET /manage/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:              ":8060",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Println("Payment Service listening on :8060")
	return server.ListenAndServe()
}
