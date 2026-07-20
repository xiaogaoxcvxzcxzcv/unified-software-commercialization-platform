BEGIN;

LOCK TABLE hosted_interaction.idempotency_records IN ACCESS EXCLUSIVE MODE;
LOCK TABLE hosted_interaction.self_service_flows IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM hosted_interaction.self_service_flows LIMIT 1) THEN
        RAISE EXCEPTION 'refusing to drop hosted self-service flow state while rows remain';
    END IF;
END
$$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM hosted_interaction.idempotency_records WHERE operation IN ('auth_flow_reset', 'cancel') LIMIT 1) THEN
        RAISE EXCEPTION 'refusing to narrow hosted idempotency operations while self-service records remain';
    END IF;
END
$$;

ALTER TABLE hosted_interaction.idempotency_records
    DROP CONSTRAINT idempotency_records_operation_check;

ALTER TABLE hosted_interaction.idempotency_records
    ADD CONSTRAINT idempotency_records_operation_check
    CHECK (operation IN ('create', 'account_complete'));

DROP TABLE hosted_interaction.self_service_flows;

COMMIT;
