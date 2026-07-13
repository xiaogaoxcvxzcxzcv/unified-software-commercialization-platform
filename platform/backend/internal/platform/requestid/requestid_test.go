package requestid

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewarePreservesSafeRequestID(t *testing.T) {
	const supplied = "gateway-12345678"
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := FromContext(r.Context()); got != supplied {
			t.Fatalf("request ID = %q", got)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(Header, supplied)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get(Header); got != supplied {
		t.Fatalf("response request ID = %q", got)
	}
}

func TestMiddlewareReplacesUnsafeRequestID(t *testing.T) {
	h := Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(Header, "bad\nvalue")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get(Header); len(got) != 32 {
		t.Fatalf("generated request ID = %q", got)
	}
}
