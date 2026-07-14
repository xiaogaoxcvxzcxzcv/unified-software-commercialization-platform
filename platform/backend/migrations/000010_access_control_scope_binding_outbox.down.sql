BEGIN;
DROP TABLE IF EXISTS access_control.outbox_events;
DROP TABLE IF EXISTS access_control.scope_binding_idempotency_records;
COMMIT;
