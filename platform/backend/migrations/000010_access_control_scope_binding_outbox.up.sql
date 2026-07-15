BEGIN;

CREATE TABLE access_control.scope_binding_idempotency_records (
    operation TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    key_digest TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'completed', 'failed')),
    binding_id TEXT,
    response_json JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (operation, actor_id, key_digest)
);

CREATE TABLE access_control.outbox_events (
    event_id TEXT PRIMARY KEY,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    dead BOOLEAN NOT NULL DEFAULT FALSE,
    last_error TEXT
);

CREATE INDEX access_control_outbox_pending_idx
    ON access_control.outbox_events (next_attempt_at, occurred_at)
    WHERE published_at IS NULL AND dead = FALSE;

COMMIT;
