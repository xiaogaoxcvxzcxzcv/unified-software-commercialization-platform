package entitlement

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

type CheckEntitlementCommand struct {
	Product           ProductContext
	Tenant            TenantContext
	User              UserContext
	RequestedFeatures []string
	DeviceID          string
	ClientObservedAt  *time.Time
}

type GrantEntitlementCommand struct {
	Admin          AdminScope
	User           UserContext
	Policy         PolicyRef
	Validity       ValidityInput
	Source         SourceRef
	IdempotencyKey string
	ReasonCode     ReasonCode
	TraceID        string
}

type MutateEntitlementCommand struct {
	Admin            AdminScope
	User             UserContext
	Policy           PolicyRef
	Validity         ValidityInput
	Source           SourceRef
	IdempotencyKey   string
	ExpectedRevision int64
	TargetGrantID    string
	ReasonCode       ReasonCode
	TraceID          string
}

func (s *Service) CheckEntitlement(ctx context.Context, command CheckEntitlementCommand) (CheckDecision, error) {
	if s == nil || s.repository == nil || !validID(command.Product.ProductID) || !validID(command.Tenant.TenantID) || !validID(command.User.UserID) || len(command.RequestedFeatures) == 0 || len(command.RequestedFeatures) > 100 {
		return CheckDecision{}, ErrInvalidArgument
	}
	if command.Tenant.ProductID != command.Product.ProductID {
		return CheckDecision{}, ErrScopeMismatch
	}
	features := make([]string, 0, len(command.RequestedFeatures))
	seen := make(map[string]struct{}, len(command.RequestedFeatures))
	for _, feature := range command.RequestedFeatures {
		if !validCode(feature, 128) {
			return CheckDecision{}, ErrInvalidArgument
		}
		if _, duplicate := seen[feature]; duplicate {
			return CheckDecision{}, ErrInvalidArgument
		}
		seen[feature] = struct{}{}
		features = append(features, feature)
	}
	if command.DeviceID != "" && !validID(command.DeviceID) {
		return CheckDecision{}, ErrInvalidArgument
	}
	decision, err := s.repository.CheckEntitlement(ctx, CheckQuery{
		ProductID: command.Product.ProductID, TenantID: command.Tenant.TenantID, UserID: command.User.UserID,
		RequestedFeatures: features, DeviceID: command.DeviceID, ClientObservedAt: command.ClientObservedAt, ServerTime: s.now().UTC(),
	})
	if err != nil {
		return CheckDecision{}, err
	}
	if decision.DecisionStage == "" {
		decision.DecisionStage = "entitlement"
	}
	if decision.ServerTime.IsZero() {
		decision.ServerTime = s.now().UTC()
	}
	return decision, nil
}

func (s *Service) GrantEntitlement(ctx context.Context, command GrantEntitlementCommand) (GrantResult, error) {
	record := WriteRecord{
		Operation:        EffectGrant,
		ProductID:        command.Admin.ProductID,
		TenantID:         command.Admin.TenantID,
		UserID:           command.User.UserID,
		PolicyID:         command.Policy.PolicyID,
		PolicyVersion:    command.Policy.Version,
		Validity:         command.Validity,
		Source:           command.Source,
		IdempotencyKey:   command.IdempotencyKey,
		ExpectedRevision: 0,
		Actor:            ActorRef{Type: ActorAdmin, ID: command.Admin.AdminID},
		ReasonCode:       defaultReason(command.ReasonCode, ReasonManualGrant),
		TraceID:          command.TraceID,
	}
	if err := s.prepareWrite(&record); err != nil {
		return GrantResult{}, err
	}
	return s.repository.GrantEntitlement(ctx, record)
}

func (s *Service) ExtendEntitlement(ctx context.Context, command MutateEntitlementCommand) (GrantResult, error) {
	record := writeRecordFromMutate(EffectExtend, command, defaultReason(command.ReasonCode, ReasonManualExtend))
	if err := s.prepareWrite(&record); err != nil {
		return GrantResult{}, err
	}
	return s.repository.ExtendEntitlement(ctx, record)
}

func (s *Service) ReplaceEntitlement(ctx context.Context, command MutateEntitlementCommand) (GrantResult, error) {
	record := writeRecordFromMutate(EffectReplace, command, defaultReason(command.ReasonCode, ReasonManualExtend))
	if err := s.prepareWrite(&record); err != nil {
		return GrantResult{}, err
	}
	return s.repository.ReplaceEntitlement(ctx, record)
}

func (s *Service) RevokeEntitlement(ctx context.Context, command MutateEntitlementCommand) (GrantResult, error) {
	record := writeRecordFromMutate(EffectRevoke, command, defaultReason(command.ReasonCode, ReasonManualRevoke))
	if err := s.prepareWrite(&record); err != nil {
		return GrantResult{}, err
	}
	if !validID(record.TargetGrantID) && (record.Source.Type == "" || !validSource(record.Source)) {
		return GrantResult{}, ErrInvalidArgument
	}
	return s.repository.RevokeEntitlement(ctx, record)
}

func (s *Service) GetCurrentEntitlements(ctx context.Context, product ProductContext, tenant TenantContext, user UserContext) (EntitlementSummary, error) {
	if s == nil || s.repository == nil || !validID(product.ProductID) || !validID(tenant.TenantID) || !validID(user.UserID) {
		return EntitlementSummary{}, ErrInvalidArgument
	}
	if tenant.ProductID != product.ProductID {
		return EntitlementSummary{}, ErrScopeMismatch
	}
	return s.repository.GetCurrentEntitlements(ctx, CurrentQuery{ProductID: product.ProductID, TenantID: tenant.TenantID, UserID: user.UserID})
}

func (s *Service) ListCurrentEntitlements(ctx context.Context, query AdminListQuery) ([]EntitlementSummary, error) {
	if s == nil || s.repository == nil || !validID(query.ProductID) || !validID(query.TenantID) || query.Limit < 1 || query.Limit > 200 {
		return nil, ErrInvalidArgument
	}
	if query.UserID != "" && !validID(query.UserID) {
		return nil, ErrInvalidArgument
	}
	if query.Cursor != "" && !validID(query.Cursor) {
		return nil, ErrInvalidArgument
	}
	return s.repository.ListCurrentEntitlements(ctx, query)
}

func (s *Service) ListHistory(ctx context.Context, query HistoryQuery) ([]LedgerEntry, error) {
	if s == nil || s.repository == nil || !validID(query.ProductID) || !validID(query.TenantID) || !validID(query.UserID) || query.Limit < 1 || query.Limit > 200 {
		return nil, ErrInvalidArgument
	}
	if query.Cursor != "" && !validID(query.Cursor) {
		return nil, ErrInvalidArgument
	}
	return s.repository.ListHistory(ctx, query)
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

func writeRecordFromMutate(operation Effect, command MutateEntitlementCommand, reason ReasonCode) WriteRecord {
	return WriteRecord{
		Operation:        operation,
		ProductID:        command.Admin.ProductID,
		TenantID:         command.Admin.TenantID,
		UserID:           command.User.UserID,
		PolicyID:         command.Policy.PolicyID,
		PolicyVersion:    command.Policy.Version,
		Validity:         command.Validity,
		Source:           command.Source,
		IdempotencyKey:   command.IdempotencyKey,
		ExpectedRevision: command.ExpectedRevision,
		TargetGrantID:    command.TargetGrantID,
		Actor:            ActorRef{Type: ActorAdmin, ID: command.Admin.AdminID},
		ReasonCode:       reason,
		TraceID:          command.TraceID,
	}
}

func (s *Service) prepareWrite(record *WriteRecord) error {
	if s == nil || s.repository == nil || s.ids == nil || len(s.digestKey) < 32 || record == nil {
		return ErrInvalidArgument
	}
	if !validOperation(record.Operation) || !validID(record.ProductID) || !validID(record.TenantID) || !validID(record.UserID) || !validID(record.Actor.ID) || !validActor(record.Actor.Type) || !validReason(record.ReasonCode) || !validID(record.TraceID) || !validID(record.IdempotencyKey) || len(record.IdempotencyKey) < 16 {
		return ErrInvalidArgument
	}
	if record.Operation != EffectRevoke {
		if !validID(record.PolicyID) || record.PolicyVersion < 1 || !validValidity(record.Validity) || !validSource(record.Source) {
			return ErrInvalidArgument
		}
	}
	if record.Operation != EffectGrant && record.ExpectedRevision < 1 {
		return ErrInvalidArgument
	}
	request, err := json.Marshal(struct {
		Operation        Effect        `json:"operation"`
		ProductID        string        `json:"product_id"`
		TenantID         string        `json:"tenant_id"`
		UserID           string        `json:"user_id"`
		PolicyID         string        `json:"policy_id,omitempty"`
		PolicyVersion    int64         `json:"policy_version,omitempty"`
		Validity         ValidityInput `json:"validity"`
		Source           SourceRef     `json:"source"`
		ExpectedRevision int64         `json:"expected_revision,omitempty"`
		TargetGrantID    string        `json:"target_grant_id,omitempty"`
		Actor            ActorRef      `json:"actor"`
		ReasonCode       ReasonCode    `json:"reason_code"`
	}{
		record.Operation, record.ProductID, record.TenantID, record.UserID, record.PolicyID, record.PolicyVersion,
		record.Validity, record.Source, record.ExpectedRevision, record.TargetGrantID, record.Actor, record.ReasonCode,
	})
	if err != nil {
		return err
	}
	keyDigest := hmacDigest(s.digestKey, []byte(record.IdempotencyKey))
	requestDigest := hmacDigest(s.digestKey, request)
	auditDigest := hmacDigest(s.digestKey, append([]byte("entitlement-audit\x00"+record.ProductID+"\x00"+record.TenantID+"\x00"+record.UserID+"\x00"), keyDigest...))
	grantID, err := s.ids.ID("entitlement_grant_")
	if err != nil {
		return err
	}
	ledgerID, err := s.ids.ID("entitlement_ledger_")
	if err != nil {
		return err
	}
	eventID, err := s.ids.ID("entitlement_event_")
	if err != nil {
		return err
	}
	record.KeyHash = keyDigest
	record.RequestHash = requestDigest
	record.AuditID = "audit_" + hex.EncodeToString(auditDigest[:16])
	record.GrantID = grantID
	record.LedgerID = ledgerID
	record.OutboxEventID = eventID
	record.Now = s.now().UTC()
	return nil
}

func hmacDigest(key, value []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}

func defaultReason(value, fallback ReasonCode) ReasonCode {
	if value == "" {
		return fallback
	}
	return value
}

func validOperation(value Effect) bool {
	switch value {
	case EffectGrant, EffectExtend, EffectReplace, EffectRevoke, EffectExpire:
		return true
	default:
		return false
	}
}

func validActor(value ActorType) bool {
	switch value {
	case ActorAdmin, ActorSystem, ActorUser:
		return true
	default:
		return false
	}
}

func validSource(value SourceRef) bool {
	switch value.Type {
	case SourceAdmin, SourceTrial, SourceGift, SourceOrder, SourceLicense:
	default:
		return false
	}
	return validID(value.SourceID) && validID(value.SourceEffectID)
}

func validValidity(value ValidityInput) bool {
	switch value.Rule {
	case ValidityFixedDuration:
		return value.Duration > 0 && value.FixedUntil.IsZero()
	case ValidityFixedEnd:
		return value.Duration == 0 && !value.FixedUntil.IsZero()
	case ValidityLifetime:
		return value.Duration == 0 && value.FixedUntil.IsZero()
	default:
		return false
	}
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

func validCode(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (i > 0 && (r == '.' || r == '_' || r == '-')) {
			continue
		}
		return false
	}
	return true
}

func validReason(value ReasonCode) bool {
	return validCode(string(value), 64)
}
