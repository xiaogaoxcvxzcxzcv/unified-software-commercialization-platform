package httptransport

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/requestid"
)

var hostedOwnedSessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

type HostedPresentation struct {
	ProductName  string  `json:"product_name"`
	ThemeVariant *string `json:"theme_variant"`
}
type HostedExternalProvider struct {
	Provider    string `json:"provider"`
	Mode        string `json:"mode"`
	DisplayName string `json:"display_name"`
}
type HostedAuthBootstrap struct {
	Interaction         Interaction              `json:"interaction"`
	Presentation        HostedPresentation       `json:"presentation"`
	Flow                HostedAuthFlow           `json:"flow"`
	PasswordEnabled     bool                     `json:"password_enabled"`
	RegistrationEnabled bool                     `json:"registration_enabled"`
	RecoveryEnabled     bool                     `json:"recovery_enabled"`
	ExternalProviders   []HostedExternalProvider `json:"external_providers"`
}
type HostedAuthFlow struct {
	Kind           string  `json:"kind"`
	IdentifierHint *string `json:"identifier_hint,omitempty"`
}
type UserProfile struct {
	UserID      string  `json:"user_id"`
	Version     int64   `json:"version"`
	DisplayName *string `json:"display_name,omitempty"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
	Locale      *string `json:"locale,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
}
type UserSessionSummary struct {
	SessionID   string    `json:"session_id"`
	Current     bool      `json:"current"`
	DeviceLabel *string   `json:"device_label,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type ExternalIdentity struct {
	ExternalIdentityID string    `json:"external_identity_id"`
	Provider           string    `json:"provider"`
	MaskedSubject      *string   `json:"masked_subject,omitempty"`
	Status             string    `json:"status"`
	LinkedAt           time.Time `json:"linked_at"`
	AuditID            *string   `json:"audit_id,omitempty"`
}
type HostedAccountBootstrap struct {
	Interaction        Interaction          `json:"interaction"`
	Presentation       HostedPresentation   `json:"presentation"`
	Profile            UserProfile          `json:"profile"`
	Sessions           []UserSessionSummary `json:"sessions"`
	ExternalIdentities []ExternalIdentity   `json:"external_identities"`
	AllowedActions     []string             `json:"allowed_actions"`
}

type Challenge struct {
	Accepted       bool   `json:"accepted"`
	ContinuationID string `json:"continuation_id"`
}
type VerificationStartCommand struct{ Identifier, IdempotencyKey, RequestID string }
type RegisterCommand struct{ Credential, Proof, DisplayName, IdempotencyKey, RequestID string }
type RecoveryStartCommand struct{ Identifier, IdempotencyKey, RequestID string }
type RecoveryCompleteCommand struct{ Proof, NewCredential, IdempotencyKey, RequestID string }
type ProfilePatchValue struct {
	Set   bool
	Value *string
}
type ProfilePatchCommand struct {
	ExpectedVersion                          int64
	DisplayName, AvatarURL, Locale, Timezone ProfilePatchValue
	IdempotencyKey, RequestID                string
}
type PasswordChangeCommand struct {
	CurrentCredential, NewCredential string
	RevokeOtherSessions              bool
	IdempotencyKey, RequestID        string
}

func (h *Handler) selfService(w http.ResponseWriter, r *http.Request) (SelfService, bool) {
	s, ok := h.service.(SelfService)
	if !ok {
		h.writeError(w, r, ErrTemporarilyUnavailable)
	}
	return s, ok
}
func (h *Handler) requireHostedRead(w http.ResponseWriter, r *http.Request, id string) (HostedPrincipal, bool) {
	if len(r.Header.Values("Authorization")) != 0 {
		h.writeError(w, r, ErrAuthenticationRequired)
		return HostedPrincipal{}, false
	}
	cookie, ok := hostedCookie(r)
	if !ok {
		h.writeError(w, r, ErrAuthenticationRequired)
		return HostedPrincipal{}, false
	}
	p, err := h.auth.ResolveHostedSession(r.Context(), id, cookie)
	if err != nil {
		if errors.Is(err, ErrInteractionExpired) {
			h.writeError(w, r, ErrInteractionExpired)
		} else if errors.Is(err, ErrSessionRevoked) || errors.Is(err, ErrAuthenticationRequired) {
			h.writeError(w, r, ErrSessionRevoked)
		} else {
			h.writeError(w, r, ErrTemporarilyUnavailable)
		}
		return HostedPrincipal{}, false
	}
	if !validHostedPrincipal(p) || p.InteractionID != id {
		h.writeError(w, r, ErrSessionRevoked)
		return HostedPrincipal{}, false
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || len(origins) == 1 && origins[0] != h.hostedOrigin {
		h.writeError(w, r, ErrCSRFFailed)
		return HostedPrincipal{}, false
	}
	return p, true
}
func (h *Handler) authBootstrap(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodGet) || !emptyBody(w, r) {
		return
	}
	p, ok := h.requireHostedRead(w, r, id)
	if !ok {
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	v, e := s.AuthBootstrap(r.Context(), p, id)
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (h *Handler) accountBootstrap(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodGet) || !emptyBody(w, r) {
		return
	}
	p, ok := h.requireHostedRead(w, r, id)
	if !ok {
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	v, e := s.AccountBootstrap(r.Context(), p, id)
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type identifierRequest struct {
	Identifier string `json:"identifier"`
}

func (h *Handler) verificationStart(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	var b identifierRequest
	if !decodeStrictJSON(w, r, &b) {
		h.writeInvalid(w, r)
		return
	}
	if !bounded(b.Identifier, 1, 320) {
		h.writeInvalidField(w, r, "identifier", "invalid_length")
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	v, e := s.StartRegistrationVerification(r.Context(), p, id, VerificationStartCommand{b.Identifier, key, requestid.FromContext(r.Context())})
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}
func (h *Handler) recoveryStart(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	var b identifierRequest
	if !decodeStrictJSON(w, r, &b) {
		h.writeInvalid(w, r)
		return
	}
	if !bounded(b.Identifier, 1, 320) {
		h.writeInvalidField(w, r, "identifier", "invalid_length")
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	v, e := s.StartRecovery(r.Context(), p, id, RecoveryStartCommand{b.Identifier, key, requestid.FromContext(r.Context())})
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

type registerRequest struct {
	Credential  string `json:"credential"`
	Proof       string `json:"verification_proof"`
	DisplayName string `json:"display_name"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	var b registerRequest
	if !decodeStrictJSON(w, r, &b) {
		h.writeInvalid(w, r)
		return
	}
	if !bounded(b.Credential, 12, 1024) {
		h.writeInvalidField(w, r, "credential", "invalid_length")
		return
	}
	if !bounded(b.Proof, 16, 512) {
		h.writeInvalidField(w, r, "verification_proof", "invalid_length")
		return
	}
	if len(b.DisplayName) > 128 {
		h.writeInvalidField(w, r, "display_name", "invalid_length")
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	v, e := s.RegisterHosted(r.Context(), p, id, RegisterCommand{b.Credential, b.Proof, b.DisplayName, key, requestid.FromContext(r.Context())})
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	if !validCompletion(v, id) {
		h.writeError(w, r, ErrTemporarilyUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type recoveryCompleteRequest struct {
	Proof         string `json:"recovery_proof"`
	NewCredential string `json:"new_credential"`
}

func (h *Handler) recoveryComplete(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	var b recoveryCompleteRequest
	if !decodeStrictJSON(w, r, &b) {
		h.writeInvalid(w, r)
		return
	}
	if !bounded(b.Proof, 16, 512) {
		h.writeInvalidField(w, r, "recovery_proof", "invalid_length")
		return
	}
	if !bounded(b.NewCredential, 12, 1024) {
		h.writeInvalidField(w, r, "new_credential", "invalid_length")
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	e := s.CompleteRecovery(r.Context(), p, id, RecoveryCompleteCommand{b.Proof, b.NewCredential, key, requestid.FromContext(r.Context())})
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) authFlow(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodDelete) || !emptyBody(w, r) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	if err := s.ResetAuthFlow(r.Context(), p, id, key); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type nullableJSONString struct {
	Set   bool
	Value *string
}

func (v *nullableJSONString) UnmarshalJSON(raw []byte) error {
	v.Set = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		v.Value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	v.Value = &s
	return nil
}

type profileRequest struct {
	ExpectedVersion int64              `json:"expected_version"`
	DisplayName     optionalJSONString `json:"display_name"`
	AvatarURL       nullableJSONString `json:"avatar_url"`
	Locale          nullableJSONString `json:"locale"`
	Timezone        nullableJSONString `json:"timezone"`
}

func validOptionalURI(value nullableJSONString) bool {
	if !value.Set || value.Value == nil {
		return true
	}
	if len(*value.Value) == 0 || len(*value.Value) > 2048 {
		return false
	}
	p, err := url.Parse(*value.Value)
	if err != nil || !p.IsAbs() || p.User != nil {
		return false
	}
	scheme := strings.ToLower(p.Scheme)
	return scheme != "javascript" && scheme != "data" && scheme != "file"
}
func validNullableMax(value nullableJSONString, max int) bool {
	return !value.Set || value.Value == nil || len(*value.Value) <= max
}

func (h *Handler) accountProfile(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPatch) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	var b profileRequest
	if !decodeStrictJSON(w, r, &b) {
		h.writeInvalid(w, r)
		return
	}
	if b.ExpectedVersion < 1 {
		h.writeInvalidField(w, r, "expected_version", "minimum")
		return
	}
	if b.DisplayName.Set && !bounded(b.DisplayName.Value, 1, 128) {
		h.writeInvalidField(w, r, "display_name", "invalid_length")
		return
	}
	if !validOptionalURI(b.AvatarURL) {
		h.writeInvalidField(w, r, "avatar_url", "invalid_uri")
		return
	}
	if !validNullableMax(b.Locale, 32) {
		h.writeInvalidField(w, r, "locale", "invalid_length")
		return
	}
	if !validNullableMax(b.Timezone, 64) {
		h.writeInvalidField(w, r, "timezone", "invalid_length")
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	v, e := s.PatchAccountProfile(r.Context(), p, id, ProfilePatchCommand{ExpectedVersion: b.ExpectedVersion, DisplayName: ProfilePatchValue{b.DisplayName.Set, b.DisplayName.Pointer()}, AvatarURL: ProfilePatchValue{b.AvatarURL.Set, b.AvatarURL.Value}, Locale: ProfilePatchValue{b.Locale.Set, b.Locale.Value}, Timezone: ProfilePatchValue{b.Timezone.Set, b.Timezone.Value}, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type passwordChangeRequest struct {
	CurrentCredential   string `json:"current_credential"`
	NewCredential       string `json:"new_credential"`
	RevokeOtherSessions bool   `json:"revoke_other_sessions"`
}

func (h *Handler) accountPassword(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	var b passwordChangeRequest
	if !decodeStrictJSON(w, r, &b) {
		h.writeInvalid(w, r)
		return
	}
	if !bounded(b.CurrentCredential, 1, 1024) {
		h.writeInvalidField(w, r, "current_credential", "invalid_length")
		return
	}
	if !bounded(b.NewCredential, 12, 1024) {
		h.writeInvalidField(w, r, "new_credential", "invalid_length")
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	if e := s.ChangeAccountPassword(r.Context(), p, id, PasswordChangeCommand{b.CurrentCredential, b.NewCredential, b.RevokeOtherSessions, key, requestid.FromContext(r.Context())}); e != nil {
		h.writeError(w, r, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) accountSession(w http.ResponseWriter, r *http.Request, id, target string) {
	if !onlyMethod(w, r, http.MethodDelete) || !emptyBody(w, r) {
		return
	}
	if !hostedOwnedSessionID.MatchString(target) {
		h.writeInvalidField(w, r, "session_id", "invalid")
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	s, ok := h.selfService(w, r)
	if !ok {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	if e := s.RevokeAccountSession(r.Context(), p, id, target, key, requestid.FromContext(r.Context())); e != nil {
		h.writeError(w, r, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
