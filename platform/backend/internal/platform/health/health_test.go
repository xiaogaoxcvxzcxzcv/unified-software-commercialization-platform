package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type probe struct {
	name string
	err  error
}

func (p probe) Name() string                { return p.name }
func (p probe) Check(context.Context) error { return p.err }

func TestReadyReportsUnavailableDependency(t *testing.T) {
	h := New([]Probe{probe{name: "postgres", err: context.DeadlineExceeded}}, time.Second, "test")
	rr := httptest.NewRecorder()
	h.Ready(rr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rr.Code)
	}
	if body := rr.Body.String(); body != "{\"checks\":{\"postgres\":\"unavailable\"},\"status\":\"not_ready\"}\n" {
		t.Fatalf("body = %s", body)
	}
}

func TestLiveRejectsPost(t *testing.T) {
	h := New(nil, time.Second, "test")
	rr := httptest.NewRecorder()
	h.Live(rr, httptest.NewRequest(http.MethodPost, "/health/live", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rr.Code)
	}
}
