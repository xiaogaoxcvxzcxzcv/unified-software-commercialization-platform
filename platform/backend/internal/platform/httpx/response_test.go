package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorFieldErrorsAreOptionalAndStructured(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	ErrorWithOptions(w, r, http.StatusBadRequest, "invalid", "invalid", ErrorOptions{})
	if strings.Contains(w.Body.String(), "field_errors") {
		t.Fatalf("unexpected field errors: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	ErrorWithOptions(w, r, http.StatusBadRequest, "invalid", "invalid", ErrorOptions{FieldErrors: []FieldError{{Field: "body", Code: "invalid"}}})
	if !strings.Contains(w.Body.String(), `"field_errors":[{"field":"body","code":"invalid"}]`) {
		t.Fatalf("body=%s", w.Body.String())
	}
}
