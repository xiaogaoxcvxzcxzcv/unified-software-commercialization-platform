BEGIN;

INSERT INTO access_control.admin_permissions (permission_code, description, risk_level)
VALUES
    ('product.application.manage', 'Create and manage product applications', 'normal'),
    ('product.application.security.manage', 'Manage application redirects, client bindings, and credentials', 'high');

INSERT INTO access_control.admin_role_permissions (role_id, permission_code)
SELECT role_id, permission_code
FROM access_control.admin_roles
CROSS JOIN (VALUES
    ('product.application.manage'),
    ('product.application.security.manage')
) AS permissions(permission_code)
WHERE role_code = 'super_admin';

UPDATE access_control.admin_authorization_versions
SET authorization_version = authorization_version + 1,
    updated_at = CURRENT_TIMESTAMP
WHERE admin_user_id IN (
    SELECT DISTINCT binding.admin_user_id
    FROM access_control.admin_scope_bindings binding
    JOIN access_control.admin_roles role ON role.role_id = binding.role_id
    WHERE role.role_code = 'super_admin' AND binding.status = 'active'
);

COMMIT;
