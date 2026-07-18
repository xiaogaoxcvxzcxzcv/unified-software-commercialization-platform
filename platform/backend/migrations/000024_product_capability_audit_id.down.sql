BEGIN;

ALTER TABLE product.product_capability_sets
    DROP CONSTRAINT IF EXISTS product_capability_sets_audit_id_shape,
    DROP COLUMN IF EXISTS audit_id;

COMMIT;
