BEGIN;

DELETE FROM access_control.admin_role_permissions
WHERE permission_code IN ('product.application.manage', 'product.application.security.manage');

DELETE FROM access_control.admin_permissions
WHERE permission_code IN ('product.application.manage', 'product.application.security.manage');

UPDATE access_control.admin_authorization_versions
SET authorization_version = authorization_version + 1,
    updated_at = CURRENT_TIMESTAMP
WHERE admin_user_id IN (
    SELECT DISTINCT binding.admin_user_id
    FROM access_control.admin_scope_bindings binding
    JOIN access_control.admin_roles role ON role.role_id = binding.role_id
    WHERE role.role_code = 'platform-super-admin' AND binding.status = 'active'
);

COMMIT;
