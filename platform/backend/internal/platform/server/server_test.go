package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"platform.local/commercialization/backend/internal/platform/health"
)

type healthyProbe struct{}

func (healthyProbe) Name() string                { return "postgres" }
func (healthyProbe) Check(context.Context) error { return nil }

func TestHandlerReturnsTrackedJSONNotFound(t *testing.T) {
	var logs bytes.Buffer
	h := NewHandler(slog.New(slog.NewJSONHandler(&logs, nil)), []health.Probe{healthyProbe{}}, time.Second, BuildInfo{Version: "test"})
	req := httptest.NewRequest(http.MethodGet, "/missing?secret=not-logged", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", rr.Header().Get("Content-Type"))
	}
	requestID := rr.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("missing request ID")
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "route_not_found" || body.Error.RequestID != requestID {
		t.Fatalf("body = %+v", body)
	}
	if bytes.Contains(logs.Bytes(), []byte("secret")) {
		t.Fatal("query string leaked into access log")
	}
}

func TestReadyReturnsOK(t *testing.T) {
	h := NewHandler(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), []health.Probe{healthyProbe{}}, time.Second, BuildInfo{Version: "test"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestRunStopsGracefullyWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app := &App{
		server: &http.Server{
			Addr:              "127.0.0.1:0",
			Handler:           http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
			ReadHeaderTimeout: time.Second,
		},
		logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), shutdownTimeout: time.Second,
	}
	if err := app.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
