package httpx

import (
	"encoding/json"
	"net/http"

	"platform.local/commercialization/backend/internal/platform/requestid"
)

type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, ErrorBody{Error: ErrorDetail{
		Code: code, Message: message, RequestID: requestid.FromContext(r.Context()),
	}})
}

func MethodNotAllowed(w http.ResponseWriter, r *http.Request, allowed string) {
	w.Header().Set("Allow", allowed)
	Error(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}
