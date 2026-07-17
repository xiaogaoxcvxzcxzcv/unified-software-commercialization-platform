package productuseraccess

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
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
	return s.setStatus(ctx, ChangeRecord{ScopeType: ScopeProduct, ProductID: command.Product.ProductID, UserID: command.User.UserID, Status: command.Status, ExpectedVersion: command.ExpectedVersion, ReasonCode: command.ReasonCode, OperatorNote: command.OperatorNote}, command.IdempotencyKey)
}

func (s *Service) SetTenantAccessStatus(ctx context.Context, command SetTenantAccessStatusCommand) (StatusChangeResult, error) {
	if !validID(command.Product.ProductID) || !validID(command.Tenant.TenantID) || !validID(command.User.UserID) {
		return StatusChangeResult{}, ErrInvalidArgument
	}
	if command.Tenant.ProductID != command.Product.ProductID {
		return StatusChangeResult{}, ErrScopeMismatch
	}
	return s.setStatus(ctx, ChangeRecord{ScopeType: ScopeTenant, ProductID: command.Product.ProductID, TenantID: command.Tenant.TenantID, UserID: command.User.UserID, Status: command.Status, ExpectedVersion: command.ExpectedVersion, ReasonCode: command.ReasonCode, OperatorNote: command.OperatorNote}, command.IdempotencyKey)
}

func (s *Service) setStatus(ctx context.Context, record ChangeRecord, idempotencyKey string) (StatusChangeResult, error) {
	if s == nil || s.repository == nil || s.ids == nil || len(s.digestKey) < 32 || (record.Status != StatusActive && record.Status != StatusSuspended) || record.ExpectedVersion < 0 || !validReason(record.ReasonCode) || !validOperatorNote(record.OperatorNote) || len(idempotencyKey) < 16 || len(idempotencyKey) > 256 {
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
	}{record.ScopeType, record.ProductID, record.TenantID, record.UserID, record.Status, record.ExpectedVersion, record.ReasonCode, record.OperatorNote})
	if err != nil {
		return StatusChangeResult{}, err
	}
	requestDigest := hmacDigest(s.digestKey, request)
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
