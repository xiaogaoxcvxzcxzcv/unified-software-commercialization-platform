BEGIN;

CREATE SCHEMA IF NOT EXISTS platform_meta;

CREATE TABLE platform_meta.installation (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    schema_generation INTEGER NOT NULL DEFAULT 1 CHECK (schema_generation > 0),
    initialized_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO platform_meta.installation (singleton, schema_generation)
VALUES (TRUE, 1)
ON CONFLICT (singleton) DO NOTHING;

COMMIT;
