package http_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	uchttp "github.com/aleksclark/ultracore/http"
)

func TestHealthzOK(t *testing.T) {
	h := uchttp.NewHandler(uchttp.Config{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz status=%d want 200", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if string(body) != "ok" {
		t.Fatalf("healthz body=%q", body)
	}
}

func TestReadyzOKWhenNoReadyHook(t *testing.T) {
	h := uchttp.NewHandler(uchttp.Config{})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("readyz status=%d want 200", rr.Code)
	}
}

func TestReadyzFailsWhenReadyErrors(t *testing.T) {
	h := uchttp.NewHandler(uchttp.Config{
		Ready: func() error { return errors.New("pg down") },
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status=%d want 503", rr.Code)
	}
}
