package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type fakeHotelReader struct {
	hotel Hotel
	err   error
	calls int
	uid   string
}

func (store *fakeHotelReader) GetHotel(_ context.Context, uid string) (Hotel, error) {
	store.calls++
	store.uid = uid
	return store.hotel, store.err
}

func TestGetHotelHandler(t *testing.T) {
	const uid = "049161bb-badd-4fa8-9d90-87c9a82b0668"
	stars := 5
	hotel := Hotel{HotelUID: uid, Name: "Ararat Park Hyatt Moscow", Country: "Россия", City: "Москва", Address: "Неглинная ул., 4", Stars: &stars, Price: 10000}
	tests := []struct {
		name, uid     string
		err           error
		status, calls int
		nullStars     bool
	}{
		{"success", uid, nil, 200, 1, false},
		{"nullable stars", uid, nil, 200, 1, true},
		{"invalid UUID", "invalid", nil, 400, 0, false},
		{"missing hotel", uid, errHotelNotFound, 404, 1, false},
		{"database failure", uid, errors.New("secret database details"), 500, 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := hotel
			if tc.nullStars {
				want.Stars = nil
			}
			store := &fakeHotelReader{hotel: want, err: tc.err}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/hotels/{hotelUid}", getHotelHandler(store))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/hotels/"+tc.uid, nil))
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if store.calls != tc.calls {
				t.Fatalf("calls = %d, want %d", store.calls, tc.calls)
			}
			if store.calls > 0 && store.uid != tc.uid {
				t.Fatal("wrong hotel requested")
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatal("database details leaked")
			}
			if tc.status == 200 {
				var got Hotel
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("hotel = %+v, want %+v", got, want)
				}
			}
		})
	}
}
