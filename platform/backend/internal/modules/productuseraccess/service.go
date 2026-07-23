package productuseraccess

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type IDGenerator interface {
	ID(prefix string) (string, error)
}

type Service struct {
	repository Repository
	ids        IDGenerator
	digestKey  []byte
	now        func() time.Time
}

func NewService(repository Repository, ids IDGenerator, digestKey []byte, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, ids: ids, digestKey: append([]byte(nil), digestKey...), now: now}
}

func (s *Service) EvaluateScopedAdmission(ctx context.Context, product ProductContext, tenant *TenantContext, user UserContext) (Admission, error) {
	if s == nil || s.repository == nil || !validID(product.ProductID) || !validID(user.UserID) {
		return Admission{}, ErrInvalidArgument
	}
	tenantID := ""
	if tenant != nil {
		if !validID(tenant.TenantID) || tenant.ProductID != product.ProductID {
			return Admission{}, ErrScopeMismatch
		}
		tenantID = tenant.TenantID
	}
	return s.repository.EvaluateScopedAdmission(ctx, product.ProductID, tenantID, user.UserID)
}

func (s *Service) SetProductAccessStatus(ctx context.Context, command SetProductAccessStatusCommand) (StatusChangeResult, error) {
	if !validID(command.Product.ProductID) || !validID(command.User.UserID) {
		return StatusChangeResult{}, ErrInvalidArgument
	}
	return s.setStatus(ctx, ChangeRecord{ScopeType: ScopeProduct, ProductID: command.Product.ProductID, UserID: command.User.UserID, Status: command.Status, ExpectedVersion: command.ExpectedVersion, ReasonCode: command.ReasonCode, OperatorNote: command.OperatorNote, ActorID: command.ActorID, TraceID: command.TraceID}, command.IdempotencyKey)
}

func (s *Service) SetTenantAccessStatus(ctx context.Context, command SetTenantAccessStatusCommand) (StatusChangeResult, error) {
	if !validID(command.Product.ProductID) || !validID(command.Tenant.TenantID) || !validID(command.User.UserID) {
		return StatusChangeResult{}, ErrInvalidArgument
	}
	if command.Tenant.ProductID != command.Product.ProductID {
		return StatusChangeResult{}, ErrScopeMismatch
	}
	return s.setStatus(ctx, ChangeRecord{ScopeType: ScopeTenant, ProductID: command.Product.ProductID, TenantID: command.Tenant.TenantID, UserID: command.User.UserID, Status: command.Status, ExpectedVersion: command.ExpectedVersion, ReasonCode: command.ReasonCode, OperatorNote: command.OperatorNote, ActorID: command.ActorID, TraceID: command.TraceID}, command.IdempotencyKey)
}

func (s *Service) setStatus(ctx context.Context, record ChangeRecord, idempotencyKey string) (StatusChangeResult, error) {
	if s == nil || s.repository == nil || s.ids == nil || len(s.digestKey) < 32 || (record.Status != StatusActive && record.Status != StatusSuspended) || record.ExpectedVersion < 0 || !validReason(record.ReasonCode) || !validOperatorNote(record.OperatorNote) || !validID(record.ActorID) || !validID(record.TraceID) || len(idempotencyKey) < 16 || len(idempotencyKey) > 256 {
		return StatusChangeResult{}, ErrInvalidArgument
	}
	keyDigest := hmacDigest(s.digestKey, []byte(idempotencyKey))
	request, err := json.Marshal(struct {
		ScopeType       ScopeType `json:"scope_type"`
		ProductID       string    `json:"product_id"`
		TenantID        string    `json:"tenant_id"`
		UserID          string    `json:"user_id"`
		Status          Status    `json:"status"`
		ExpectedVersion int64     `json:"expected_version"`
		ReasonCode      string    `json:"reason_code"`
		OperatorNote    string    `json:"operator_note"`
		ActorID         string    `json:"actor_id"`
	}{record.ScopeType, record.ProductID, record.TenantID, record.UserID, record.Status, record.ExpectedVersion, record.ReasonCode, record.OperatorNote, record.ActorID})
	if err != nil {
		return StatusChangeResult{}, err
	}
	requestDigest := hmacDigest(s.digestKey, request)
	auditInput := []byte("product-user-access-audit\x00" + string(record.ScopeType) + "\x00" + record.ProductID + "\x00" + record.TenantID + "\x00" + record.UserID + "\x00")
	auditDigest := hmacDigest(s.digestKey, append(auditInput, keyDigest...))
	record.AuditID = "audit_" + hex.EncodeToString(auditDigest[:16])
	statusEventID, err := s.ids.ID("product_user_access_event_")
	if err != nil {
		return StatusChangeResult{}, err
	}
	revocationEventID := ""
	if record.Status == StatusSuspended {
		revocationEventID, err = s.ids.ID("product_user_access_event_")
		if err != nil {
			return StatusChangeResult{}, err
		}
	}
	record.KeyDigest, record.RequestDigest = keyDigest, requestDigest
	record.StatusEventID, record.RevocationEventID, record.Now = statusEventID, revocationEventID, s.now().UTC()
	if record.ScopeType == ScopeProduct {
		return s.repository.SetProductAccessStatus(ctx, record)
	}
	return s.repository.SetTenantAccessStatus(ctx, record)
}

func hmacDigest(key, value []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}

func (s *Service) ListScopedUserIDs(ctx context.Context, query ListScopedUserIDsQuery) ([]string, error) {
	if s == nil || s.repository == nil || !validID(query.Product.ProductID) {
		return nil, ErrInvalidArgument
	}
	tenantID := ""
	if query.Tenant != nil {
		if !validID(query.Tenant.TenantID) || query.Tenant.ProductID != query.Product.ProductID {
			return nil, ErrScopeMismatch
		}
		tenantID = query.Tenant.TenantID
	}
	return s.repository.ListScopedUserIDs(ctx, query.Product.ProductID, tenantID)
}

func (s *Service) GetScopedAccessBatch(ctx context.Context, query GetScopedAccessBatchQuery) ([]ScopedAccess, error) {
	if s == nil || s.repository == nil || !validID(query.Product.ProductID) || len(query.UserIDs) == 0 || len(query.UserIDs) > 200 {
		return nil, ErrInvalidArgument
	}
	tenantID := ""
	scopeType, scopeID := ScopeProduct, query.Product.ProductID
	if query.Tenant != nil {
		if !validID(query.Tenant.TenantID) || query.Tenant.ProductID != query.Product.ProductID {
			return nil, ErrScopeMismatch
		}
		tenantID, scopeType, scopeID = query.Tenant.TenantID, ScopeTenant, query.Tenant.TenantID
	}
	seen := make(map[string]struct{}, len(query.UserIDs))
	userIDs := make([]string, 0, len(query.UserIDs))
	for _, userID := range query.UserIDs {
		if !validID(userID) {
			return nil, ErrInvalidArgument
		}
		if _, duplicate := seen[userID]; duplicate {
			return nil, ErrInvalidArgument
		}
		seen[userID] = struct{}{}
		userIDs = append(userIDs, userID)
	}
	facts, err := s.repository.GetScopedAccessBatch(ctx, query.Product.ProductID, tenantID, userIDs)
	if err != nil {
		return nil, err
	}
	byUser := make(map[string]AccessFact, len(facts))
	for _, fact := range facts {
		if fact.ProductID != query.Product.ProductID || fact.TenantID != tenantID || fact.ScopeType != scopeType {
			return nil, ErrScopeMismatch
		}
		if _, requested := seen[fact.UserID]; !requested || (fact.Status != StatusActive && fact.Status != StatusSuspended) || fact.AccessVersion < 1 {
			return nil, ErrInvalidArgument
		}
		if _, duplicate := byUser[fact.UserID]; duplicate {
			return nil, ErrInvalidArgument
		}
		byUser[fact.UserID] = fact
	}
	result := make([]ScopedAccess, 0, len(userIDs))
	for _, userID := range userIDs {
		item := ScopedAccess{ScopeType: scopeType, ScopeID: scopeID, ProductID: query.Product.ProductID, TenantID: tenantID, UserID: userID, Status: StatusActive}
		if fact, ok := byUser[userID]; ok {
			changedAt := fact.StatusChangedAt
			item.Status, item.Explicit, item.AccessVersion, item.StatusChangedAt = fact.Status, true, fact.AccessVersion, &changedAt
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Service) ClaimOutbox(ctx context.Context, limit int) ([]ClaimedOutboxEvent, error) {
	if s == nil || s.repository == nil || limit < 1 || limit > 200 {
		return nil, ErrInvalidArgument
	}
	return s.repository.ClaimOutbox(ctx, s.now().UTC(), limit)
}

func (s *Service) MarkOutboxPublished(ctx context.Context, eventID string) error {
	if s == nil || s.repository == nil || !validID(eventID) {
		return ErrInvalidArgument
	}
	return s.repository.MarkOutboxPublished(ctx, eventID, s.now().UTC())
}

func (s *Service) MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	summary = strings.TrimSpace(summary)
	if s == nil || s.repository == nil || !validID(eventID) || summary == "" || next.IsZero() {
		return ErrInvalidArgument
	}
	runes := []rune(summary)
	if len(runes) > 500 {
		summary = string(runes[:500])
	}
	return s.repository.MarkOutboxFailed(ctx, eventID, summary, next.UTC(), dead)
}

func validID(value string) bool {
	trimmed := strings.TrimSpace(value)
	if value != trimmed || value == "" || len(value) > 160 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validReason(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9') || (i > 0 && (r == '.' || r == '_' || r == '-')) {
			continue
		}
		return false
	}
	return true
}

func validOperatorNote(value string) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 500 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
