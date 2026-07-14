BEGIN;

CREATE SCHEMA IF NOT EXISTS access_control;

CREATE TABLE access_control.admin_roles (
    role_id TEXT PRIMARY KEY,
    role_code TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE access_control.admin_permissions (
    permission_code TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    risk_level TEXT NOT NULL CHECK (risk_level IN ('normal', 'high'))
);

CREATE TABLE access_control.admin_role_permissions (
    role_id TEXT NOT NULL REFERENCES access_control.admin_roles(role_id),
    permission_code TEXT NOT NULL REFERENCES access_control.admin_permissions(permission_code),
    PRIMARY KEY (role_id, permission_code)
);

CREATE TABLE access_control.admin_scope_bindings (
    binding_id TEXT PRIMARY KEY,
    admin_user_id TEXT NOT NULL,
    role_id TEXT NOT NULL REFERENCES access_control.admin_roles(role_id),
    scope_type TEXT NOT NULL CHECK (scope_type IN ('platform', 'product', 'tenant')),
    scope_id TEXT,
    product_id TEXT,
    tenant_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    effective_from TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (
        (scope_type = 'platform' AND scope_id IS NULL AND product_id IS NULL AND tenant_id IS NULL) OR
        (scope_type = 'product' AND scope_id IS NOT NULL AND product_id = scope_id AND tenant_id IS NULL) OR
        (scope_type = 'tenant' AND scope_id IS NOT NULL AND product_id IS NOT NULL AND tenant_id = scope_id)
    )
);

CREATE INDEX admin_scope_bindings_user_active_idx
    ON access_control.admin_scope_bindings (admin_user_id, effective_from, expires_at)
    WHERE status = 'active';

CREATE UNIQUE INDEX admin_scope_bindings_identity_idx
    ON access_control.admin_scope_bindings (admin_user_id, role_id, scope_type, COALESCE(scope_id, ''));

CREATE TABLE access_control.admin_authorization_versions (
    admin_user_id TEXT PRIMARY KEY,
    authorization_version BIGINT NOT NULL CHECK (authorization_version > 0),
    updated_at TIMESTAMPTZ NOT NULL
);

COMMIT;
