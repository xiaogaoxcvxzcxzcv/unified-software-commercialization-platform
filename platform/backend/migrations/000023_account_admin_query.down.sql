BEGIN;

DROP INDEX IF EXISTS product_user_access.product_access_scope_status_idx;
DROP INDEX IF EXISTS identity.end_user_sessions_user_scope_activity_idx;
DROP INDEX IF EXISTS identity.end_user_sessions_tenant_membership_idx;
DROP INDEX IF EXISTS identity.end_user_sessions_product_membership_idx;
DROP INDEX IF EXISTS identity.user_profiles_display_name_prefix_idx;
DROP INDEX IF EXISTS identity.users_account_status_user_idx;

COMMIT;
