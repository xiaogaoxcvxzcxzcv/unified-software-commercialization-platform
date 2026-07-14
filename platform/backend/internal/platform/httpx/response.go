package httpx

import (
	"encoding/json"
	"net/http"

	"platform.local/capability-platform/backend/internal/platform/requestid"
)

type ErrorBody struct {
	Type              string `json:"type"`
	Title             string `json:"title"`
	Status            int    `json:"status"`
	Code              string `json:"code"`
	Detail            string `json:"detail,omitempty"`
	RequestID         string `json:"request_id"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds *int   `json:"retry_after_seconds,omitempty"`
}

type ErrorOptions struct {
	Retryable         bool
	RetryAfterSeconds *int
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	ErrorWithOptions(w, r, status, code, message, ErrorOptions{Retryable: status >= http.StatusInternalServerError})
}

func ErrorWithOptions(w http.ResponseWriter, r *http.Request, status int, code, message string, options ErrorOptions) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorBody{
		Type:              "about:blank",
		Title:             http.StatusText(status),
		Status:            status,
		Code:              code,
		Detail:            message,
		RequestID:         requestid.FromContext(r.Context()),
		Retryable:         options.Retryable,
		RetryAfterSeconds: options.RetryAfterSeconds,
	})
}

func MethodNotAllowed(w http.ResponseWriter, r *http.Request, allowed string) {
	w.Header().Set("Allow", allowed)
	Error(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}
