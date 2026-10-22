package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

const loyaltyRetryInterval = 10 * time.Second

type loyaltyRetryJob struct{ username, reservationUID, authorization string }

func (client bookingClient) decrementLoyalty(ctx context.Context, job loyaltyRetryJob) error {
	if job.authorization != "" {
		ctx = context.WithValue(ctx, requestIdentityKey{}, requestIdentity{
			Subject: job.username, Authorization: job.authorization,
		})
	}
	return client.callWithKey(ctx, http.MethodPatch, client.loyaltyURL, "/api/v1/loyalty",
		job.username, map[string]int{"reservationCountChange": -1}, nil, "cancel:"+job.reservationUID)
}

// Jobs live only in this Gateway process. The worker starts when first needed.
type loyaltyRetryQueue struct {
	client          bookingClient
	interval        time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	jobs            map[string]loyaltyRetryJob
	started, closed bool
	workers         sync.WaitGroup
}

func newLoyaltyRetryQueue(client bookingClient, interval time.Duration) *loyaltyRetryQueue {
	ctx, cancel := context.WithCancel(context.Background())
	return &loyaltyRetryQueue{client: client, interval: interval, ctx: ctx, cancel: cancel,
		jobs: make(map[string]loyaltyRetryJob)}
}

func (queue *loyaltyRetryQueue) enqueue(job loyaltyRetryJob) error {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.closed {
		return errors.New("loyalty retry queue is closed")
	}
	if _, exists := queue.jobs[job.reservationUID]; exists {
		return nil
	}
	queue.jobs[job.reservationUID] = job
	log.Printf("Queued loyalty update for canceled reservation %s", job.reservationUID)
	if !queue.started {
		queue.started = true
		queue.workers.Add(1)
		go queue.run()
	}
	return nil
}

func (queue *loyaltyRetryQueue) run() {
	defer queue.workers.Done()
	ticker := time.NewTicker(queue.interval)
	defer ticker.Stop()
	for {
		select {
		case <-queue.ctx.Done():
			return
		case <-ticker.C:
			queue.mu.Lock()
			jobs := make([]loyaltyRetryJob, 0, len(queue.jobs))
			for _, job := range queue.jobs {
				jobs = append(jobs, job)
			}
			queue.mu.Unlock()
			for _, job := range jobs {
				if queue.ctx.Err() != nil {
					return
				}
				ctx, cancel := context.WithTimeout(queue.ctx, 6*time.Second)
				err := queue.client.decrementLoyalty(ctx, job)
				cancel()
				if err != nil {
					log.Printf("Retry loyalty update for %s failed: %v", job.reservationUID, err)
					continue
				}
				queue.mu.Lock()
				delete(queue.jobs, job.reservationUID)
				queue.mu.Unlock()
				log.Printf("Completed queued loyalty update for %s", job.reservationUID)
			}
		}
	}
}

func (queue *loyaltyRetryQueue) Close() {
	queue.mu.Lock()
	queue.closed = true
	queue.cancel()
	queue.mu.Unlock()
	queue.workers.Wait()
}

type gatewayHandler struct {
	http.Handler
	retries *loyaltyRetryQueue
}

func (handler *gatewayHandler) Close() { handler.retries.Close() }
