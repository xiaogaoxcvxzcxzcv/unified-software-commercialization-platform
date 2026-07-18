BEGIN;

ALTER TABLE product.product_capability_sets
    ADD COLUMN audit_id TEXT;

UPDATE product.product_capability_sets capability_set
SET audit_id = (
    SELECT payload->>'audit_id'
    FROM product.outbox_events
    WHERE aggregate_id = capability_set.product_id
      AND payload->>'target_id' = capability_set.capability_set_id
      AND payload->>'audit_id' IS NOT NULL
    ORDER BY occurred_at DESC, event_id DESC
    LIMIT 1
)
WHERE capability_set.audit_id IS NULL;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM product.product_capability_sets WHERE audit_id IS NULL) THEN
        RAISE EXCEPTION 'cannot backfill audit_id for every product capability set';
    END IF;
END $$;

ALTER TABLE product.product_capability_sets
    ALTER COLUMN audit_id SET NOT NULL,
    ADD CONSTRAINT product_capability_sets_audit_id_shape
        CHECK (audit_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$');

COMMIT;
