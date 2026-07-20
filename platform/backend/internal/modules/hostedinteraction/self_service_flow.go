package hostedinteraction

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type HostedAuthFlow struct {
	Kind           string  `json:"kind"`
	IdentifierHint *string `json:"identifier_hint,omitempty"`
}
type SelfServiceFlow struct {
	InteractionID, Kind, IdentifierHint string
	Protected                           ProtectedState
	Version                             int64
	CreatedAt, UpdatedAt, ExpiresAt     time.Time
}
type PutSelfServiceFlowRecord struct {
	InteractionID, Kind, IdentifierHint string
	Protected                           ProtectedState
	TTL                                 time.Duration
}
type ResetSelfServiceFlowRecord struct {
	InteractionID                         string
	ActorDigest, KeyDigest, RequestDigest []byte
}
type SelfServiceFlowRepository interface {
	PutSelfServiceFlow(context.Context, PutSelfServiceFlowRecord) (SelfServiceFlow, error)
	GetSelfServiceFlow(context.Context, string) (SelfServiceFlow, bool, error)
	DeleteSelfServiceFlow(context.Context, string) error
	ResetSelfServiceFlowIdempotent(context.Context, ResetSelfServiceFlowRecord) error
}
type selfServiceSecret struct {
	Identifier   string `json:"identifier"`
	Continuation string `json:"continuation"`
}

func (s *Service) authFlow(ctx context.Context, v Interaction) (HostedAuthFlow, error) {
	flow, found, err := s.flows.GetSelfServiceFlow(ctx, v.InteractionID)
	if err != nil {
		return HostedAuthFlow{}, err
	}
	if !found {
		return HostedAuthFlow{Kind: "login"}, nil
	}
	hint := flow.IdentifierHint
	return HostedAuthFlow{Kind: flow.Kind, IdentifierHint: &hint}, nil
}
func (s *Service) persistAuthFlow(ctx context.Context, v Interaction, kind, identifier, continuation string) (HostedAuthFlow, error) {
	raw, _ := json.Marshal(selfServiceSecret{Identifier: identifier, Continuation: continuation})
	protected, err := s.protector.Protect(ctx, StateContext{InteractionID: v.InteractionID, Route: v.Route, Scope: v.Scope, TraceID: v.TraceID}, string(raw))
	if err != nil {
		return HostedAuthFlow{}, err
	}
	flow, err := s.flows.PutSelfServiceFlow(ctx, PutSelfServiceFlowRecord{InteractionID: v.InteractionID, Kind: kind, IdentifierHint: safeIdentifierHint(identifier), Protected: protected, TTL: s.browserTTL})
	if err != nil {
		return HostedAuthFlow{}, err
	}
	hint := flow.IdentifierHint
	return HostedAuthFlow{Kind: flow.Kind, IdentifierHint: &hint}, nil
}
func (s *Service) revealAuthFlow(ctx context.Context, v Interaction, kind string) (selfServiceSecret, error) {
	flow, found, err := s.flows.GetSelfServiceFlow(ctx, v.InteractionID)
	if err != nil {
		return selfServiceSecret{}, err
	}
	if !found || flow.Kind != kind {
		return selfServiceSecret{}, ErrInvalidGrant
	}
	raw, err := s.protector.Reveal(ctx, StateContext{InteractionID: v.InteractionID, Route: v.Route, Scope: v.Scope, TraceID: v.TraceID}, flow.Protected)
	if err != nil {
		return selfServiceSecret{}, ErrInvalidGrant
	}
	var secret selfServiceSecret
	if json.Unmarshal([]byte(raw), &secret) != nil || strings.TrimSpace(secret.Identifier) == "" || strings.TrimSpace(secret.Continuation) == "" {
		return selfServiceSecret{}, ErrInvalidGrant
	}
	return secret, nil
}
func safeIdentifierHint(value string) string {
	value = strings.TrimSpace(value)
	if at := strings.Index(value, "@"); at > 0 {
		r := []rune(value[:at])
		return string(r[0]) + "***" + value[at:]
	}
	r := []rune(value)
	if len(r) <= 4 {
		return "***"
	}
	return "***" + string(r[len(r)-4:])
}
func (s *Service) ResetAuthFlow(ctx context.Context, interactionID, browserToken, key string) error {
	if len(key) < 16 || len(key) > 128 {
		return ErrInvalidArgument
	}
	_, err := s.selfServiceAccess(ctx, interactionID, browserToken, RouteAuth)
	if err != nil {
		return err
	}
	return s.flows.ResetSelfServiceFlowIdempotent(ctx, ResetSelfServiceFlowRecord{
		InteractionID: interactionID,
		ActorDigest:   s.digest("auth-flow-reset-actor", browserToken),
		KeyDigest:     s.digest("idempotency-key", key),
		RequestDigest: digestJSON(struct {
			InteractionID string
			Route         Route
		}{InteractionID: interactionID, Route: RouteAuth}),
	})
}
