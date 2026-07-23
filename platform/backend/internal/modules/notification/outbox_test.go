package notification

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type outboxRepositoryStub struct {
	claimed   []ClaimedSecurityOutboxEvent
	published []ClaimedSecurityOutboxEvent
	retried   []ClaimedSecurityOutboxEvent
	dead      []ClaimedSecurityOutboxEvent
	delays    []time.Duration
	codes     []string
}

func (r *outboxRepositoryStub) ClaimSecurityOutbox(_ context.Context, workerID string, _ time.Duration) (ClaimedSecurityOutboxEvent, error) {
	if len(r.claimed) == 0 {
		return ClaimedSecurityOutboxEvent{}, ErrNotFound
	}
	event := r.claimed[0]
	r.claimed = r.claimed[1:]
	if event.LeaseToken == "" {
		event.LeaseToken = "lease:" + workerID
	}
	return event, nil
}

func (r *outboxRepositoryStub) find(eventID, token string) ClaimedSecurityOutboxEvent {
	return ClaimedSecurityOutboxEvent{EventID: eventID, LeaseToken: token}
}

func (r *outboxRepositoryStub) MarkSecurityOutboxPublished(_ context.Context, eventID, token string) error {
	r.published = append(r.published, r.find(eventID, token))
	return nil
}

func (r *outboxRepositoryStub) MarkSecurityOutboxRetry(_ context.Context, eventID, token string, delay time.Duration, code string) error {
	r.retried = append(r.retried, r.find(eventID, token))
	r.delays = append(r.delays, delay)
	r.codes = append(r.codes, code)
	return nil
}

func (r *outboxRepositoryStub) MarkSecurityOutboxDead(_ context.Context, eventID, token, code string) error {
	r.dead = append(r.dead, r.find(eventID, token))
	r.codes = append(r.codes, code)
	return nil
}

type outboxSinkStub struct {
	events []ClaimedSecurityOutboxEvent
	err    error
}

func (s *outboxSinkStub) Publish(_ context.Context, event ClaimedSecurityOutboxEvent) error {
	s.events = append(s.events, event)
	return s.err
}

func TestOutboxDispatcherPublishesRetriesAndDiesAtBound(t *testing.T) {
	base := ClaimedSecurityOutboxEvent{EventID: "event-1", DeliveryID: "delivery-1", EventType: "notification.security-delivery-requested.v1", Payload: []byte(`{"delivery_id":"delivery-1"}`), AttemptCount: 1}

	t.Run("published", func(t *testing.T) {
		repository := &outboxRepositoryStub{claimed: []ClaimedSecurityOutboxEvent{base}}
		sink := &outboxSinkStub{}
		dispatcher, _ := NewOutboxDispatcher(repository, sink, "outbox-worker")
		if !dispatcher.DispatchOne(context.Background()) || len(repository.published) != 1 || len(repository.retried) != 0 || len(repository.dead) != 0 {
			t.Fatalf("published=%+v retried=%+v dead=%+v", repository.published, repository.retried, repository.dead)
		}
	})

	t.Run("retry is bounded and error is redacted", func(t *testing.T) {
		repository := &outboxRepositoryStub{claimed: []ClaimedSecurityOutboxEvent{base}}
		sink := &outboxSinkStub{err: errors.New("private@example.com private-proof")}
		dispatcher, _ := NewOutboxDispatcher(repository, sink, "outbox-worker")
		if !dispatcher.DispatchOne(context.Background()) || len(repository.retried) != 1 || repository.delays[0] != time.Second {
			t.Fatalf("retried=%+v delays=%+v", repository.retried, repository.delays)
		}
		if repository.codes[0] != "NOTIFICATION_OUTBOX_SINK_UNAVAILABLE" || strings.Contains(repository.codes[0], "private") {
			t.Fatalf("unsafe error code=%q", repository.codes[0])
		}
	})

	t.Run("last attempt becomes dead", func(t *testing.T) {
		last := base
		last.AttemptCount = 5
		repository := &outboxRepositoryStub{claimed: []ClaimedSecurityOutboxEvent{last}}
		dispatcher, _ := NewOutboxDispatcher(repository, &outboxSinkStub{err: errors.New("unavailable")}, "outbox-worker")
		if !dispatcher.DispatchOne(context.Background()) || len(repository.dead) != 1 || len(repository.retried) != 0 {
			t.Fatalf("dead=%+v retried=%+v", repository.dead, repository.retried)
		}
	})
}

func TestOutboxPayloadContainsNoSecurityPlaintext(t *testing.T) {
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	repository := &securityRepositoryStub{}
	service, _ := NewService(repository, testProtector(t, "outbox-payload-key", 0x44), testDigester(t), providerRegistryStub{enabled: map[string]bool{"provider-a": true}, idempotent: true}, func() time.Time { return now })
	command := testSecurityCommand(now)
	if err := service.EnqueueSecurityDelivery(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	payload := string(repository.records[command.DeliveryID].Event.Payload)
	assertNoSecuritySecrets(t, payload, command.Destination, command.Proof)
}
