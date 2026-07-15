BEGIN;

DELETE FROM access_control.admin_role_permissions
WHERE permission_code IN ('assembly.blueprint.manage','assembly.plan','assembly.execute','assembly.read');

DELETE FROM access_control.admin_permissions
WHERE permission_code IN ('assembly.blueprint.manage','assembly.plan','assembly.execute','assembly.read');

DROP SCHEMA IF EXISTS assembly CASCADE;

COMMIT;
