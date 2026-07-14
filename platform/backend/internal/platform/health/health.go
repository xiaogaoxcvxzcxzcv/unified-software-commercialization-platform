package health

import (
	"context"
	"net/http"
	"time"

	"platform.local/capability-platform/backend/internal/platform/httpx"
)

type Probe interface {
	Name() string
	Check(context.Context) error
}

type Handler struct {
	probes  []Probe
	timeout time.Duration
	version string
}

func New(probes []Probe, timeout time.Duration, version string) Handler {
	return Handler{probes: probes, timeout: timeout, version: version}
}

func (h Handler) Live(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.MethodNotAllowed(w, r, http.MethodGet)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok", "version": h.version})
}

func (h Handler) Ready(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.MethodNotAllowed(w, r, http.MethodGet)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	checks := make(map[string]string, len(h.probes))
	ready := true
	for _, probe := range h.probes {
		if err := probe.Check(ctx); err != nil {
			checks[probe.Name()] = "unavailable"
			ready = false
		} else {
			checks[probe.Name()] = "ok"
		}
	}
	status := http.StatusOK
	state := "ready"
	if !ready {
		status = http.StatusServiceUnavailable
		state = "not_ready"
	}
	httpx.JSON(w, status, map[string]any{"status": state, "checks": checks})
}
