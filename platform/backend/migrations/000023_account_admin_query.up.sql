BEGIN;

CREATE INDEX users_account_status_user_idx
    ON identity.users (account_status, user_id);

CREATE INDEX user_profiles_display_name_prefix_idx
    ON identity.user_profiles (lower(display_name) text_pattern_ops, user_id);

CREATE INDEX end_user_sessions_product_membership_idx
    ON identity.end_user_sessions (product_id, user_id, created_at, session_id);

CREATE INDEX end_user_sessions_tenant_membership_idx
    ON identity.end_user_sessions (product_id, tenant_id, user_id, created_at, session_id)
    WHERE tenant_id IS NOT NULL;

CREATE INDEX end_user_sessions_user_scope_activity_idx
    ON identity.end_user_sessions (user_id, product_id, tenant_id, last_seen_at DESC, session_id);

CREATE INDEX product_access_scope_status_idx
    ON product_user_access.product_access (product_id, status, user_id);

COMMIT;
