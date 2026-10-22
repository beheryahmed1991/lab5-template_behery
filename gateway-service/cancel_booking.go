package main

import (
	"context"
	"errors"
	"hash/fnv"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func (client bookingClient) cancelBookingHandler(retries *loyaltyRetryQueue) http.HandlerFunc {
	// Serialize cancellation of the same UUID in this Gateway process.
	// Fixed lock buckets avoid retaining a mutex for every reservation forever.
	var locks [64]sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get("X-User-Name")
		if strings.TrimSpace(username) == "" || !utf8.ValidString(username) || utf8.RuneCountInString(username) > 80 {
			gatewayError(w, 400, "X-User-Name is required and must contain at most 80 characters")
			return
		}
		uid := strings.ToLower(r.PathValue("reservationUid"))
		if !validBookingUUID(uid) {
			gatewayError(w, 400, "reservationUid must be a valid UUID")
			return
		}
		hash := fnv.New32a()
		hash.Write([]byte(uid))
		lock := &locks[hash.Sum32()%uint32(len(locks))]
		lock.Lock()
		defer lock.Unlock()
		if r.Context().Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		var reservation struct {
			Status     string `json:"status"`
			PaymentUID string `json:"paymentUid"`
		}
		path := "/api/v1/reservations/" + uid
		// Reservation Service filters by both UUID and username before any mutation.
		if err := client.call(ctx, http.MethodGet, client.reservationURL, path, username, nil, &reservation); err != nil {
			var failure serviceError
			if errors.As(err, &failure) && failure.status == 404 {
				gatewayError(w, 404, "Reservation not found")
			} else {
				gatewayError(w, 503, "Reservation Service unavailable")
			}
			return
		}
		if reservation.Status == "CANCELED" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if reservation.Status != "PAID" || !validBookingUUID(reservation.PaymentUID) {
			gatewayError(w, 503, "Invalid Reservation Service response")
			return
		}
		// Finish the bounded workflow even if the caller disconnects after it starts.
		operationCtx, operationCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
		defer operationCancel()
		if err := client.call(operationCtx, http.MethodDelete, client.paymentURL, "/api/v1/payments/"+reservation.PaymentUID, "", nil, nil); err != nil {
			gatewayError(w, 503, "Unable to cancel payment")
			return
		}
		// Cancel the reservation before touching loyalty, so a failed cancellation
		// cannot reduce the user's counter. Confirm an ambiguous DELETE response.
		if err := client.call(operationCtx, http.MethodDelete, client.reservationURL, path, username, nil, nil); err != nil {
			var current struct {
				Status string `json:"status"`
			}
			recoveryCtx, recoveryCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 12*time.Second)
			defer recoveryCancel()
			readErr := client.call(recoveryCtx, http.MethodGet, client.reservationURL, path, username, nil, &current)
			if readErr != nil || current.Status != "CANCELED" {
				log.Printf("Unable to confirm cancellation for %s: %v", uid, readErr)
				gatewayError(w, 503, "Unable to cancel reservation")
				return
			}
		}
		job := loyaltyRetryJob{username: username, reservationUID: uid,
			authorization: identityFromContext(r.Context()).Authorization}
		if err := client.decrementLoyalty(operationCtx, job); err != nil {
			log.Printf("Deferring loyalty update for %s: %v", uid, err)
			if err := retries.enqueue(job); err != nil {
				gatewayError(w, 503, "Unable to queue loyalty update")
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
