package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBearer(t *testing.T) {
	auth := NewBearer("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, test := range []struct {
		name   string
		header string
		want   int
	}{
		{"valid", "Bearer abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", http.StatusNoContent},
		{"missing", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic abcdef", http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

func TestBearerRateLimitsFailures(t *testing.T) {
	auth := NewBearer("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	var last int
	for range 21 {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		last = response.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("21st failure status = %d", last)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid request during failure limit = %d", response.Code)
	}
}
