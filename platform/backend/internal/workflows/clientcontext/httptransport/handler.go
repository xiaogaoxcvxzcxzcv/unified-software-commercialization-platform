package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/modules/tenant"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
	"platform.local/capability-platform/backend/internal/workflows/clientcontext"
)

type Service interface {
	CreateSession(context.Context, clientcontext.CreateCommand) (clientcontext.Session, error)
}

type Handler struct{ service Service }

func New(service Service) *Handler { return &Handler{service: service} }

type proofEnvelope struct {
	SchemaVersion int       `json:"schema_version"`
	Type          string    `json:"type"`
	Value         string    `json:"value"`
	Timestamp     time.Time `json:"timestamp"`
}
type proofSummary struct {
	SchemaVersion int    `json:"schema_version"`
	Digest        string `json:"digest"`
}
type createRequest struct {
	ClientID      string        `json:"client_id"`
	CredentialID  string        `json:"credential_id"`
	ClientProof   proofEnvelope `json:"client_proof"`
	ClientVersion string        `json:"client_version"`
	DeviceSummary *proofSummary `json:"device_summary"`
	ChannelProof  *proofSummary `json:"channel_proof"`
	RequestNonce  string        `json:"request_nonce"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/client/session" {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if r.Method != http.MethodPost {
		httpx.MethodNotAllowed(w, r, http.MethodPost)
		return
	}
	if h.service == nil || r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "client_session.invalid_request", "invalid client session request")
		return
	}
	var body createRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ClientProof.SchemaVersion != 1 || (body.ClientProof.Type != "hmac_sha256_v1" && body.ClientProof.Type != "ed25519_signature_v1") || len(body.ClientProof.Value) < 32 || len(body.ClientProof.Value) > 1024 || (body.ChannelProof != nil && (body.ChannelProof.SchemaVersion != 1 || !strings.HasPrefix(body.ChannelProof.Digest, "sha256:"))) {
		httpx.Error(w, r, http.StatusBadRequest, "client_session.invalid_request", "invalid client session request")
		return
	}
	channelProof := ""
	if body.ChannelProof != nil {
		channelProof = body.ChannelProof.Digest
	}
	result, err := h.service.CreateSession(r.Context(), clientcontext.CreateCommand{
		ClientID: body.ClientID, CredentialID: body.CredentialID, ProofType: body.ClientProof.Type,
		ProofValue: body.ClientProof.Value, ProofTimestamp: body.ClientProof.Timestamp,
		ClientVersion: body.ClientVersion, RequestNonce: body.RequestNonce,
		ChannelProof: channelProof, TraceID: requestid.FromContext(r.Context()),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusCreated, result)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		httpx.Error(w, r, status, "client_session.invalid_request", "invalid client session request")
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		httpx.Error(w, r, http.StatusBadRequest, "client_session.invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, clientcontext.ErrInvalidClientContext) {
		httpx.Error(w, r, http.StatusBadRequest, "client_session.invalid_request", "invalid client session request")
		return
	}
	if errors.Is(err, product.ErrNotFound) || errors.Is(err, product.ErrProductUnavailable) || errors.Is(err, product.ErrClientUnavailable) || errors.Is(err, product.ErrCredentialUnavailable) || errors.Is(err, product.ErrNonceReplayed) || errors.Is(err, productapplication.ErrContextRejected) || errors.Is(err, productapplication.ErrApplicationSuspended) || errors.Is(err, tenant.ErrTenantNotFound) || errors.Is(err, tenant.ErrTenantSuspended) || errors.Is(err, tenant.ErrInvalidTenantProof) {
		httpx.Error(w, r, http.StatusUnauthorized, "client_session.invalid_client", "client authentication failed")
		return
	}
	httpx.Error(w, r, http.StatusInternalServerError, "client_session.unavailable", "client session service is temporarily unavailable")
}
