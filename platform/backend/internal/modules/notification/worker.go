package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

type Worker struct {
	repository SecurityRepository
	protector  SecurityPayloadProtector
	digester   SecurityDigestPort
	gateway    SecurityProviderGateway
	secrets    SecretResolver
	workerID   string
	now        func() time.Time
	lease      time.Duration
	timeout    time.Duration
	poll       time.Duration
}

func NewWorker(repository SecurityRepository, protector SecurityPayloadProtector, digester SecurityDigestPort, gateway SecurityProviderGateway, secrets SecretResolver, workerID string, now func() time.Time) (*Worker, error) {
	if repository == nil || protector == nil || digester == nil || gateway == nil || secrets == nil || !validIdentifier(workerID, 1, 160) {
		return nil, ErrProviderUnavailable
	}
	if now == nil {
		now = time.Now
	}
	return &Worker{repository: repository, protector: protector, digester: digester, gateway: gateway, secrets: secrets, workerID: workerID, now: now, lease: time.Minute, timeout: 10 * time.Second, poll: 500 * time.Millisecond}, nil
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if !w.RunOne(ctx) {
			timer.Reset(w.poll)
		} else {
			timer.Reset(0)
		}
	}
}

func (w *Worker) RunOne(ctx context.Context) bool {
	delivery, err := w.repository.ClaimSecurityDelivery(ctx, w.workerID, w.lease)
	if err != nil {
		return false
	}
	attempt := SecurityDeliveryAttempt{AttemptID: attemptID(delivery.DeliveryID, delivery.AttemptCount), DeliveryID: delivery.DeliveryID, AttemptNumber: delivery.AttemptCount, StartedAt: delivery.LeaseStartedAt}
	result, deliveryErr := w.deliver(ctx, delivery)
	finished := w.now().UTC()
	attempt.FinishedAt = finished
	nextStatus := "delivered"
	if deliveryErr == nil {
		attempt.Outcome = "delivered"
		if result.MessageRef != "" {
			receiptDigest, digestErr := w.digester.Digest(ctx, "notification.security.provider-message.v1", delivery.ProviderRef, delivery.DeliveryID, result.MessageRef)
			if digestErr != nil || len(receiptDigest) != 32 {
				deliveryErr = ErrProviderUnavailable
			} else {
				attempt.ProviderMessageDigest = receiptDigest
			}
		}
	}
	if deliveryErr != nil {
		code, terminal := deliveryErrorCode(deliveryErr)
		attempt.ErrorCode = &code
		digest := sha256.Sum256([]byte(code))
		attempt.ErrorDigest = digest[:]
		if terminal || delivery.AttemptCount >= delivery.MaxAttempts || !delivery.ExpiresAt.After(finished) {
			attempt.Outcome, nextStatus = "terminal_failure", "dead"
		} else {
			attempt.Outcome, nextStatus = "retryable_failure", "pending"
		}
	}
	delay := time.Duration(1<<min(delivery.AttemptCount-1, 6)) * time.Second
	record := CompleteSecurityDeliveryRecord{DeliveryID: delivery.DeliveryID, LeaseOwner: w.workerID, Attempt: attempt, NextStatus: nextStatus, RetryDelay: delay}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return w.repository.CompleteSecurityDelivery(finishCtx, record) == nil
}

func (w *Worker) deliver(ctx context.Context, delivery SecurityDelivery) (SecurityProviderResult, error) {
	if !delivery.ExpiresAt.After(w.now().UTC()) {
		return SecurityProviderResult{}, ErrDeliveryRejected
	}
	callCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	capability, err := w.gateway.RequireSecurityProvider(callCtx, delivery.ProviderRef)
	if err != nil || !capability.DeliveryIDIdempotent {
		return SecurityProviderResult{}, ErrProviderUnavailable
	}
	payload, err := w.protector.Open(callCtx, payloadContextFromDelivery(delivery), delivery.Payload)
	if err != nil {
		if errors.Is(err, ErrProviderUnavailable) {
			return SecurityProviderResult{}, ErrProviderUnavailable
		}
		return SecurityProviderResult{}, ErrPayloadUnavailable
	}
	secret, err := w.secrets.ResolveSecret(callCtx, delivery.ProviderRef)
	if err != nil || len(secret) == 0 {
		return SecurityProviderResult{}, ErrProviderUnavailable
	}
	defer clear(secret)
	result, err := w.gateway.DeliverSecurity(callCtx, SecurityProviderRequest{
		DeliveryID: delivery.DeliveryID, Purpose: delivery.Purpose, ProductID: delivery.ProductID,
		ApplicationID: delivery.ApplicationID, TenantID: cloneString(delivery.TenantID), ProviderRef: delivery.ProviderRef,
		DestinationType: delivery.DestinationType, Destination: payload.Destination, Proof: payload.Proof,
		ExpiresAt: delivery.ExpiresAt, TraceID: delivery.TraceID, Secret: secret,
	})
	if err != nil {
		return SecurityProviderResult{}, normalizeProviderError(err)
	}
	return result, nil
}

func normalizeProviderError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrDeliveryRejected):
		return ErrDeliveryRejected
	case errors.Is(err, ErrPayloadUnavailable):
		return ErrPayloadUnavailable
	default:
		return ErrProviderUnavailable
	}
}

func deliveryErrorCode(err error) (string, bool) {
	if errors.Is(err, ErrDeliveryRejected) {
		return "NOTIFICATION_SECURITY_DELIVERY_REJECTED", true
	}
	if errors.Is(err, ErrPayloadUnavailable) {
		return "NOTIFICATION_SECURITY_PAYLOAD_UNAVAILABLE", true
	}
	return "NOTIFICATION_SECURITY_PROVIDER_UNAVAILABLE", false
}

func attemptID(deliveryID string, attempt int) string {
	digest := sha256.Sum256([]byte(deliveryID + "\x00" + strconv.Itoa(attempt)))
	return "nat_" + hex.EncodeToString(digest[:16])
}
