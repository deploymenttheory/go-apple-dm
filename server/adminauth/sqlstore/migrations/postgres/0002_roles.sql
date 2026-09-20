-- +up
CREATE TABLE admin_roles (
 name VARCHAR(64) NOT NULL PRIMARY KEY,
 description TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE admin_principal_roles (
 principal_name VARCHAR(64) NOT NULL,
 role_name VARCHAR(64) NOT NULL,
 PRIMARY KEY (principal_name, role_name),
 FOREIGN KEY (principal_name) REFERENCES admin_principals(name) ON DELETE CASCADE,
 FOREIGN KEY (role_name) REFERENCES admin_roles(name)
);
ALTER TABLE admin_policies ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE admin_policy_version ADD COLUMN initialized BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE admin_policy_version ADD COLUMN roles_migrated BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE admin_policy_version SET initialized = TRUE WHERE EXISTS (SELECT 1 FROM admin_principals);
-- +down
ALTER TABLE admin_policy_version DROP COLUMN roles_migrated;
ALTER TABLE admin_policy_version DROP COLUMN initialized;
ALTER TABLE admin_policies DROP COLUMN active;
DROP TABLE admin_principal_roles;
DROP TABLE admin_roles;
