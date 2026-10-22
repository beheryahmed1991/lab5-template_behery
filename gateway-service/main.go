package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	handler, err := newAuthenticatedGatewayHandler(os.Getenv("RESERVATION_SERVICE_URL"), os.Getenv("LOYALTY_SERVICE_URL"), os.Getenv("PAYMENT_SERVICE_URL"))
	if err != nil {
		return err
	}
	defer handler.(*gatewayHandler).Close()
	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	log.Println("Gateway Service listening on :8080")
	return server.ListenAndServe()
}

func newGatewayHandler(reservationURL, loyaltyURL, paymentURL string) (http.Handler, error) {
	reservationProxy, err := newServiceProxy(reservationURL, "RESERVATION_SERVICE_URL", "Reservation Service")
	if err != nil {
		return nil, err
	}
	loyaltyProxy, err := newServiceProxy(loyaltyURL, "LOYALTY_SERVICE_URL", "Loyalty Service")
	if err != nil {
		return nil, err
	}
	if _, err := newServiceProxy(paymentURL, "PAYMENT_SERVICE_URL", "Payment Service"); err != nil {
		return nil, err
	}
	transport := newReadBreakerTransport(nil)
	reservationProxy.Transport = transport
	loyaltyProxy.Transport = transport
	client := bookingClient{reservationURL: reservationURL, loyaltyURL: loyaltyURL, paymentURL: paymentURL, http: &http.Client{Timeout: 6 * time.Second, Transport: transport}}
	retries := newLoyaltyRetryQueue(client, loyaltyRetryInterval)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/reservations", client.createBooking)
	mux.HandleFunc("GET /api/v1/reservations", client.listBookingInfo)
	mux.HandleFunc("GET /api/v1/me", client.getUserInfo)
	mux.HandleFunc("GET /api/v1/reservations/{reservationUid}", client.getBookingInfo)
	mux.HandleFunc("DELETE /api/v1/reservations/{reservationUid}", client.cancelBookingHandler(retries))
	mux.HandleFunc("GET /manage/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/hotels", proxyWithTimeout(reservationProxy))
	mux.HandleFunc("GET /api/v1/loyalty", proxyWithTimeout(loyaltyProxy))
	return &gatewayHandler{Handler: mux, retries: retries}, nil
}

func newServiceProxy(serviceURL, variable, serviceName string) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(serviceURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("%s must be a valid HTTP or HTTPS URL", variable)
	}
	return &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Header.Del("X-User-Name")
			request.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("%s request failed: %v", serviceName, err)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(map[string]string{"message": serviceName + " unavailable"}); err != nil {
				log.Printf("write error response: %v", err)
			}
		},
	}, nil
}

func proxyWithTimeout(proxy *httputil.ReverseProxy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		proxy.ServeHTTP(w, r.WithContext(ctx))
	}
}
