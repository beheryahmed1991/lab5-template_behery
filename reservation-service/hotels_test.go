package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPaginationContract(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		page   int64
		size   int64
		offset int64
		bad    bool
	}{
		{name: "defaults", page: 1, size: 10},
		{name: "assignment example", query: "page=1&size=1", page: 1, size: 1},
		{name: "schema minimum", query: "page=0&size=1", page: 0, size: 1},
		{name: "second page", query: "page=2&size=10", page: 2, size: 10, offset: 10},
		{name: "maximum size", query: "size=100", page: 1, size: 100},
		{name: "negative page", query: "page=-1", bad: true},
		{name: "fractional page", query: "page=1.5", bad: true},
		{name: "invalid page", query: "page=abc", bad: true},
		{name: "zero size", query: "size=0", bad: true},
		{name: "oversized page", query: "size=101", bad: true},
		{name: "offset overflow", query: "page=9223372036854775807&size=100", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			page, size, offset, err := parsePagination(query)
			if tt.bad {
				if err == nil {
					t.Fatal("expected invalid pagination to be rejected")
				}
				return
			}
			if err != nil || page != tt.page || size != tt.size || offset != tt.offset {
				t.Fatalf("got (%d, %d, %d, %v), want (%d, %d, %d, nil)", page, size, offset, err, tt.page, tt.size, tt.offset)
			}
		})
	}
}

func TestHotelsRejectsInvalidSizeBeforeDatabaseAccess(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/hotels?size=101", nil)
	response := httptest.NewRecorder()
	hotelsHandler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", response.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("error response must be JSON: %v", err)
	}
	if body["message"] == "" {
		t.Fatal("error response must explain the invalid request")
	}
}
