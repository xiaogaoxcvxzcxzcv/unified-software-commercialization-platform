BEGIN;

DROP TRIGGER IF EXISTS audit_events_no_update ON audit.events;
DROP FUNCTION IF EXISTS audit.reject_event_mutation();
DROP TABLE IF EXISTS audit.events;
DROP SCHEMA IF EXISTS audit;

COMMIT;
