package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

type queryRepositoryStub struct {
	query  RepositoryQuery
	events []Event
	err    error
	calls  int
}

func (s *queryRepositoryStub) Query(_ context.Context, query RepositoryQuery) ([]Event, error) {
	s.calls++
	s.query = query
	return s.events, s.err
}

type authorizerStub struct {
	command  AuthorizationCommand
	decision AuthorizationDecision
	err      error
	calls    int
}

func (s *authorizerStub) AuthorizeAdmin(_ context.Context, command AuthorizationCommand) (AuthorizationDecision, error) {
	s.calls++
	s.command = command
	return s.decision, s.err
}

func TestSearchAuditEventsAuthorizesScopeAppliesDefaultAndRedacts(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	repository := &queryRepositoryStub{events: []Event{
		{AuditID: "aud-3", OccurredAt: now, ActorID: "admin-1", ProductID: "product-1", TenantID: "tenant-1", Action: "read", TargetType: "order", TargetID: "digest", Result: "success", TraceID: "trace-1", RiskLevel: "normal", RedactedSummary: map[string]any{"safe": "visible", "authorization_version": float64(7), "access_token": "hidden", "client_proof": "hidden", "nested": map[string]any{"password": "hidden", "csrf_value": "hidden", "count": float64(2)}}},
	}}
	authorizer := &authorizerStub{decision: AuthorizationDecision{Allowed: true}}
	service := NewQueryService(repository, authorizer)

	page, err := service.SearchAuditEvents(context.Background(), SearchCommand{
		AdminUserID: "admin-1", SessionID: "session-1",
		TargetScope: Scope{Type: "tenant", ProductID: "product-1", TenantID: "tenant-1"},
		TraceID:     " trace-1 ",
	})
	if err != nil {
		t.Fatalf("SearchAuditEvents() error = %v", err)
	}
	if authorizer.command.Permission != AuditReadPermission || authorizer.command.TargetScope.TenantID != "tenant-1" {
		t.Fatalf("authorization command = %+v", authorizer.command)
	}
	if repository.query.Limit != DefaultPageLimit+1 || repository.query.TraceID != "trace-1" {
		t.Fatalf("repository query = %+v", repository.query)
	}
	if len(page.Items) != 1 || page.Items[0].RedactedSummary["safe"] != "visible" {
		t.Fatalf("page = %+v", page)
	}
	if _, exists := page.Items[0].RedactedSummary["access_token"]; exists {
		t.Fatal("top-level token was not removed")
	}
	if _, exists := page.Items[0].RedactedSummary["client_proof"]; exists {
		t.Fatal("controlled-client proof was not removed")
	}
	if page.Items[0].RedactedSummary["authorization_version"] != float64(7) {
		t.Fatalf("authorization version was removed: %#v", page.Items[0].RedactedSummary)
	}
	nested, ok := page.Items[0].RedactedSummary["nested"].(map[string]any)
	if !ok || nested["count"] != float64(2) {
		t.Fatalf("nested summary = %#v", page.Items[0].RedactedSummary["nested"])
	}
	if _, exists := nested["password"]; exists {
		t.Fatal("nested password was not removed")
	}
	if _, exists := nested["csrf_value"]; exists {
		t.Fatal("nested CSRF value was not removed")
	}
}

func TestSearchAuditEventsUsesOpaqueStableCursor(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 123, time.UTC)
	repository := &queryRepositoryStub{events: []Event{
		{AuditID: "aud-3", OccurredAt: now},
		{AuditID: "aud-2", OccurredAt: now},
		{AuditID: "aud-1", OccurredAt: now.Add(-time.Second)},
	}}
	service := NewQueryService(repository, &authorizerStub{decision: AuthorizationDecision{Allowed: true}})
	page, err := service.SearchAuditEvents(context.Background(), SearchCommand{AdminUserID: "admin", SessionID: "session", TargetScope: Scope{Type: "platform"}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page = %+v", page)
	}
	position, err := decodeCursor(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if position.AuditID != "aud-2" || !position.OccurredAt.Equal(now) {
		t.Fatalf("cursor position = %+v", position)
	}

	repository.events = nil
	_, err = service.SearchAuditEvents(context.Background(), SearchCommand{AdminUserID: "admin", SessionID: "session", TargetScope: Scope{Type: "platform"}, Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if repository.query.After == nil || repository.query.After.AuditID != "aud-2" {
		t.Fatalf("after = %+v", repository.query.After)
	}
}

func TestSearchAuditEventsRejectsInvalidInputBeforeRepository(t *testing.T) {
	tests := []struct {
		name    string
		command SearchCommand
		want    error
	}{
		{name: "limit", command: SearchCommand{AdminUserID: "admin", SessionID: "session", TargetScope: Scope{Type: "platform"}, Limit: MaxPageLimit + 1}, want: ErrInvalidLimit},
		{name: "cursor", command: SearchCommand{AdminUserID: "admin", SessionID: "session", TargetScope: Scope{Type: "platform"}, Cursor: "not-a-cursor"}, want: ErrInvalidCursor},
		{name: "scope", command: SearchCommand{AdminUserID: "admin", SessionID: "session", TargetScope: Scope{Type: "tenant", TenantID: "tenant"}}, want: ErrInvalidScope},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &queryRepositoryStub{}
			service := NewQueryService(repository, &authorizerStub{decision: AuthorizationDecision{Allowed: true}})
			_, err := service.SearchAuditEvents(context.Background(), test.command)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if repository.calls != 0 {
				t.Fatal("repository called for invalid query")
			}
		})
	}
}

func TestSearchAuditEventsDenialDoesNotQueryRepository(t *testing.T) {
	repository := &queryRepositoryStub{}
	authorizer := &authorizerStub{decision: AuthorizationDecision{Allowed: false, ReasonCode: "scope_mismatch"}}
	service := NewQueryService(repository, authorizer)
	_, err := service.SearchAuditEvents(context.Background(), SearchCommand{AdminUserID: "admin", SessionID: "session", TargetScope: Scope{Type: "product", ProductID: "product-1"}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v", err)
	}
	if repository.calls != 0 || authorizer.calls != 1 {
		t.Fatalf("calls: repository=%d authorizer=%d", repository.calls, authorizer.calls)
	}
}
