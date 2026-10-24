-- +up
-- Open installs the MySQL fence idempotently using metadata inspection. DDL
-- implicitly commits; the encrypted evidence migration activates it afterward.
SELECT '@FENCE_SHA256@';
-- +down
DROP TRIGGER IF EXISTS inventory_evidence_insert;
DROP TRIGGER IF EXISTS inventory_evidence_update;
SET @inventory_evidence_ddl = (SELECT IF(COUNT(*) = 0, 'SELECT 1', 'ALTER TABLE inventory_lock DROP COLUMN evidence_model') FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'inventory_lock' AND column_name = 'evidence_model');
PREPARE inventory_evidence_remove FROM @inventory_evidence_ddl;
EXECUTE inventory_evidence_remove;
DEALLOCATE PREPARE inventory_evidence_remove;
