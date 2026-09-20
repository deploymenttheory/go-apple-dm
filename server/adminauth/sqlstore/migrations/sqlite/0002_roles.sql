-- +up
CREATE TABLE admin_roles (
 name TEXT NOT NULL PRIMARY KEY,
 description TEXT NOT NULL,
 created_at TIMESTAMP NOT NULL,
 updated_at TIMESTAMP NOT NULL
);
CREATE TABLE admin_principal_roles (
 principal_name TEXT NOT NULL,
 role_name TEXT NOT NULL,
 PRIMARY KEY (principal_name, role_name),
 FOREIGN KEY (principal_name) REFERENCES admin_principals(name) ON DELETE CASCADE,
 FOREIGN KEY (role_name) REFERENCES admin_roles(name)
);
ALTER TABLE admin_policies ADD COLUMN active INTEGER NOT NULL DEFAULT 1;
ALTER TABLE admin_policy_version ADD COLUMN initialized INTEGER NOT NULL DEFAULT 0;
ALTER TABLE admin_policy_version ADD COLUMN roles_migrated INTEGER NOT NULL DEFAULT 0;
UPDATE admin_policy_version SET initialized = 1 WHERE EXISTS (SELECT 1 FROM admin_principals);
-- +down
ALTER TABLE admin_policy_version DROP COLUMN roles_migrated;
ALTER TABLE admin_policy_version DROP COLUMN initialized;
ALTER TABLE admin_policies DROP COLUMN active;
DROP TABLE admin_principal_roles;
DROP TABLE admin_roles;
