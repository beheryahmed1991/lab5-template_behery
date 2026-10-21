package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Hotel struct {
	HotelUID string `json:"hotelUid"`
	Name     string `json:"name"`
	Country  string `json:"country"`
	City     string `json:"city"`
	Address  string `json:"address"`
	Stars    *int   `json:"stars"`
	Price    int    `json:"price"`
}

type HotelsPage struct {
	Page          int64   `json:"page"`
	PageSize      int64   `json:"pageSize"`
	TotalElements int64   `json:"totalElements"`
	Items         []Hotel `json:"items"`
}

func parsePagination(query url.Values) (page, size, offset int64, err error) {
	page, size = 1, 10
	if value := query.Get("page"); value != "" {
		page, err = strconv.ParseInt(value, 10, 64)
		if err != nil || page < 0 {
			return 0, 0, 0, fmt.Errorf("page must be a non-negative integer")
		}
	}
	if value := query.Get("size"); value != "" {
		size, err = strconv.ParseInt(value, 10, 64)
		if err != nil || size < 1 || size > 100 {
			return 0, 0, 0, fmt.Errorf("size must be an integer between 1 and 100")
		}
	}

	// The specification permits page=0, while its example uses page=1.
	// Both select the first page; subsequent pages use one-based numbering.
	if page > 1 {
		const maxInt64 = 1<<63 - 1
		if page-1 > maxInt64/size {
			return 0, 0, 0, fmt.Errorf("page is too large")
		}
		offset = (page - 1) * size
	}
	return page, size, offset, nil
}

func hotelsHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, size, offset, err := parsePagination(r.URL.Query())
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		result := HotelsPage{Page: page, PageSize: size, Items: make([]Hotel, 0)}
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM hotels").Scan(&result.TotalElements); err != nil {
			log.Printf("count hotels: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Unable to load hotels")
			return
		}

		rows, err := pool.Query(ctx, `
			SELECT hotel_uid::text, name, country, city, address, stars, price
			FROM hotels
			ORDER BY id
			LIMIT $1 OFFSET $2`, size, offset)
		if err != nil {
			log.Printf("query hotels: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Unable to load hotels")
			return
		}
		defer rows.Close()

		for rows.Next() {
			var hotel Hotel
			if err := rows.Scan(&hotel.HotelUID, &hotel.Name, &hotel.Country, &hotel.City, &hotel.Address, &hotel.Stars, &hotel.Price); err != nil {
				log.Printf("scan hotel: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "Unable to load hotels")
				return
			}
			result.Items = append(result.Items, hotel)
		}
		if err := rows.Err(); err != nil {
			log.Printf("read hotels: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Unable to load hotels")
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			log.Printf("write hotels response: %v", err)
		}
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"message": message}); err != nil {
		log.Printf("write error response: %v", err)
	}
}
