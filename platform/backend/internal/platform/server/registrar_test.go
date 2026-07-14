package server

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestModuleRegistrarRoutesMultipleModulesThroughPlatformMiddleware(t *testing.T) {
	modules := NewModuleRegistrar()
	if err := modules.Register("/api/v1/alpha/", moduleResponse("alpha")); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	if err := modules.Register("/api/v1/beta/", moduleResponse("beta")); err != nil {
		t.Fatalf("register beta: %v", err)
	}

	handler := NewHandler(
		slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		nil,
		time.Second,
		BuildInfo{Version: "test"},
		modules,
	)

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/api/v1/alpha/items", want: "alpha"},
		{path: "/api/v1/beta/items", want: "beta"},
	} {
		t.Run(test.want, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, test.path, nil))
			if rr.Code != http.StatusOK || rr.Body.String() != test.want {
				t.Fatalf("response = %d %q", rr.Code, rr.Body.String())
			}
			assertPlatformHeaders(t, rr)
		})
	}
}

func TestModuleRegistrarRejectsDuplicateAndInvalidRegistrations(t *testing.T) {
	modules := &ModuleRegistrar{}
	handler := moduleResponse("ok")
	if err := modules.Register("/api/v1/example/", handler); err != nil {
		t.Fatalf("register valid module: %v", err)
	}
	if err := modules.Register("/api/v1/example/", handler); !errors.Is(err, ErrDuplicateModulePrefix) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := modules.Register("/api/v1/nil/", nil); !errors.Is(err, ErrNilModuleHandler) {
		t.Fatalf("nil handler error = %v", err)
	}

	for _, prefix := range []string{
		"",
		"/",
		"api/v1/example/",
		"/api/v1/example",
		"/api//v1/example/",
		"/api/v1/../example/",
		"/api/v1/example/?query=true",
		"/api/v1/{example}/",
		"/api/v1/%2e%2e/example/",
		"/health/",
		"/health/custom/",
	} {
		t.Run(prefix, func(t *testing.T) {
			if err := NewModuleRegistrar().Register(prefix, handler); !errors.Is(err, ErrInvalidModulePrefix) {
				t.Fatalf("Register(%q) error = %v", prefix, err)
			}
		})
	}
}

func TestModuleRegistrarIsSealedWhenPlatformHandlerIsBuilt(t *testing.T) {
	modules := NewModuleRegistrar()
	NewHandler(
		slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		nil,
		time.Second,
		BuildInfo{Version: "test"},
		modules,
	)

	if err := modules.Register("/api/v1/late/", moduleResponse("late")); !errors.Is(err, ErrModuleRegistrarSealed) {
		t.Fatalf("late registration error = %v", err)
	}
}

func moduleResponse(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
}

func assertPlatformHeaders(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	} {
		if got := rr.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if rr.Header().Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID")
	}
}
