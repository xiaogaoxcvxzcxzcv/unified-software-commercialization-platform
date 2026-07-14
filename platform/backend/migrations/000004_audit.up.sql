BEGIN;

CREATE SCHEMA IF NOT EXISTS audit;

CREATE TABLE audit.events (
    audit_id TEXT PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    actor_id TEXT NOT NULL,
    permission TEXT,
    scope_type TEXT CHECK (scope_type IN ('platform', 'product', 'tenant')),
    scope_id TEXT,
    product_id TEXT,
    tenant_id TEXT,
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL,
    result TEXT NOT NULL,
    reason_code TEXT,
    trace_id TEXT NOT NULL,
    risk_level TEXT NOT NULL CHECK (risk_level IN ('normal', 'high')),
    redacted_summary JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX audit_events_occurred_idx ON audit.events (occurred_at DESC, audit_id DESC);
CREATE INDEX audit_events_actor_idx ON audit.events (actor_id, occurred_at DESC);
CREATE INDEX audit_events_scope_idx ON audit.events (scope_type, scope_id, occurred_at DESC);
CREATE INDEX audit_events_trace_idx ON audit.events (trace_id);

CREATE FUNCTION audit.reject_event_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit events are append-only';
END;
$$;

CREATE TRIGGER audit_events_no_update
BEFORE UPDATE OR DELETE ON audit.events
FOR EACH ROW EXECUTE FUNCTION audit.reject_event_mutation();

COMMIT;
