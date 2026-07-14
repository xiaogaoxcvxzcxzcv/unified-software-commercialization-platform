package identity

import (
	"context"
	"log/slog"
	"time"
)

type OutboxDispatcher struct {
	repository Repository
	audit      AuditPort
	logger     *slog.Logger
	interval   time.Duration
	batchSize  int
	now        func() time.Time
}

func NewOutboxDispatcher(repository Repository, auditPort AuditPort, logger *slog.Logger) *OutboxDispatcher {
	return &OutboxDispatcher{repository: repository, audit: auditPort, logger: logger, interval: time.Second, batchSize: 50, now: time.Now}
}

func (d *OutboxDispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		d.dispatch(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *OutboxDispatcher) dispatch(ctx context.Context) {
	events, err := d.repository.ClaimOutbox(ctx, d.now().UTC(), d.batchSize)
	if err != nil {
		d.logger.Error("claim identity outbox", "error", err)
		return
	}
	for _, event := range events {
		_, err := d.audit.AppendSecurityEvent(ctx, event.Payload)
		if err == nil {
			if markErr := d.repository.MarkOutboxPublished(ctx, event.EventID, d.now().UTC()); markErr != nil {
				d.logger.Error("mark identity outbox published", "event_id", event.EventID, "error", markErr)
			}
			continue
		}
		dead := event.AttemptCount >= 9
		next := d.now().UTC().Add(time.Duration(1<<min(event.AttemptCount, 6)) * time.Second)
		if markErr := d.repository.MarkOutboxFailed(ctx, event.EventID, "audit append failed", next, dead); markErr != nil {
			d.logger.Error("mark identity outbox failed", "event_id", event.EventID, "error", markErr)
		}
	}
}
