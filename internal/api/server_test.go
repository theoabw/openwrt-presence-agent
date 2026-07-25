package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/auth"
	"github.com/theoabw/openwrt-presence-agent/internal/config"
	"github.com/theoabw/openwrt-presence-agent/internal/engine"
	"github.com/theoabw/openwrt-presence-agent/pkg/protocol"
)

const testToken = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"

func testServer(t *testing.T) (*Server, *engine.Engine) {
	t.Helper()
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 2, QueueSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	server := New(config.Default(), state, auth.NewBearer(testToken), "00112233445566778899aabbccddeeff", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	return server, state
}

func request(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestAPIRequiresAuthentication(t *testing.T) {
	server, _ := testServer(t)
	if got := request(t, server.http.Handler, "/v1/info", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("status = %d", got)
	}
	if got := request(t, server.http.Handler, "/v1/info", testToken).Code; got != http.StatusOK {
		t.Fatalf("status = %d", got)
	}
}

func TestHealthReadinessAndDegradation(t *testing.T) {
	server, state := testServer(t)
	if got := request(t, server.http.Handler, "/v1/health", testToken).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("initial status = %d", got)
	}
	state.SetProvider(protocol.Provider{
		ID: "ubus-hostapd", Status: "healthy", SnapshotSupported: true,
		LastSnapshotAt: time.Now().UTC(),
	})
	if got := request(t, server.http.Handler, "/v1/health", testToken).Code; got != http.StatusOK {
		t.Fatalf("healthy status = %d", got)
	}
	state.SetProvider(protocol.Provider{
		ID: "ubus-hostapd", Status: "unavailable", SnapshotSupported: true,
		LastSnapshotAt: time.Now().UTC(),
	})
	response := request(t, server.http.Handler, "/v1/health", testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("degraded status = %d", response.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "degraded" {
		t.Fatalf("health body = %#v", body)
	}
}

func TestOnlyReadMethodsAreExposed(t *testing.T) {
	server, _ := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/clients", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	response := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(response, req)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", response.Code)
	}
}
