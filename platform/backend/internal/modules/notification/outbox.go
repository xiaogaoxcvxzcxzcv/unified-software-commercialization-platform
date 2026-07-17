package notification

import (
	"context"
	"errors"
	"time"
)

var ErrOutboxLeaseLost = errors.New("notification outbox lease lost")

const SecurityOutboxMaxAttempts = 5

type ClaimedSecurityOutboxEvent struct {
	EventID      string
	DeliveryID   string
	EventType    string
	Payload      []byte
	OccurredAt   time.Time
	AttemptCount int
	LeaseToken   string
}

type SecurityOutboxRepository interface {
	ClaimSecurityOutbox(context.Context, string, time.Duration) (ClaimedSecurityOutboxEvent, error)
	MarkSecurityOutboxPublished(context.Context, string, string) error
	MarkSecurityOutboxRetry(context.Context, string, string, time.Duration, string) error
	MarkSecurityOutboxDead(context.Context, string, string, string) error
}

type SecurityOutboxSink interface {
	// Publish must use EventID as its idempotency key because a database failure
	// after a successful publish can cause the same event to be delivered again.
	Publish(context.Context, ClaimedSecurityOutboxEvent) error
}

type OutboxDispatcher struct {
	repository  SecurityOutboxRepository
	sink        SecurityOutboxSink
	workerID    string
	maxAttempts int
	lease       time.Duration
	timeout     time.Duration
	poll        time.Duration
}

func NewOutboxDispatcher(repository SecurityOutboxRepository, sink SecurityOutboxSink, workerID string) (*OutboxDispatcher, error) {
	if repository == nil || sink == nil || !validIdentifier(workerID, 1, 160) {
		return nil, ErrProviderUnavailable
	}
	return &OutboxDispatcher{repository: repository, sink: sink, workerID: workerID, maxAttempts: SecurityOutboxMaxAttempts, lease: time.Minute, timeout: 10 * time.Second, poll: 500 * time.Millisecond}, nil
}

func (d *OutboxDispatcher) Run(ctx context.Context) {
	if d == nil {
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
		if d.DispatchOne(ctx) {
			timer.Reset(0)
		} else {
			timer.Reset(d.poll)
		}
	}
}

func (d *OutboxDispatcher) DispatchOne(ctx context.Context) bool {
	if d == nil {
		return false
	}
	event, err := d.repository.ClaimSecurityOutbox(ctx, d.workerID, d.lease)
	if err != nil {
		return false
	}
	if event.EventID == "" || event.LeaseToken == "" || event.AttemptCount < 1 {
		return false
	}
	publishCtx, cancel := context.WithTimeout(ctx, d.timeout)
	err = d.sink.Publish(publishCtx, event)
	cancel()
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if err == nil {
		return d.repository.MarkSecurityOutboxPublished(finishCtx, event.EventID, event.LeaseToken) == nil
	}
	const code = "NOTIFICATION_OUTBOX_SINK_UNAVAILABLE"
	if event.AttemptCount >= d.maxAttempts {
		return d.repository.MarkSecurityOutboxDead(finishCtx, event.EventID, event.LeaseToken, code) == nil
	}
	delay := time.Duration(1<<min(event.AttemptCount-1, 6)) * time.Second
	return d.repository.MarkSecurityOutboxRetry(finishCtx, event.EventID, event.LeaseToken, delay, code) == nil
}
