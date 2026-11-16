package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsingPagination(t *testing.T) {
	var paginationParsingTests = []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
		wantErr    bool
	}{
		{"defaults", "", 50, 0, false},
		{"only limit", "limit=100", 100, 0, false},
		{"only offset", "offset=20", 50, 20, false},
		{"limit and offset", "limit=100&offset=20", 100, 20, false},
		{"empty limit", "limit=", 50, 0, false},
		{"empty offset", "offset=", 50, 0, false},
		{"overflow limit", "limit=500", -1, -1, true},
		{"negative offset", "offset=-1", -1, -1, true},
		{"trash in limit and offset", "offset=abc&limit=abc", -1, -1, true},
	}
	for _, tt := range paginationParsingTests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			limit, offset, err := parsePagination(req)

			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if limit != tt.wantLimit || offset != tt.wantOffset {
				t.Fatalf("got (%d, %d) want (%d, %d)", limit, offset, tt.wantLimit, tt.wantOffset)
			}
		})
	}
}
