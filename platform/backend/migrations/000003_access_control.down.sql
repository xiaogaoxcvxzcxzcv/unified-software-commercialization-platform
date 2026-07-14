BEGIN;

DROP TABLE IF EXISTS access_control.admin_authorization_versions;
DROP TABLE IF EXISTS access_control.admin_scope_bindings;
DROP TABLE IF EXISTS access_control.admin_role_permissions;
DROP TABLE IF EXISTS access_control.admin_permissions;
DROP TABLE IF EXISTS access_control.admin_roles;
DROP SCHEMA IF EXISTS access_control;

COMMIT;
