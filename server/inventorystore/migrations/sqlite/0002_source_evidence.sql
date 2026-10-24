-- +up
ALTER TABLE inventory_lock ADD COLUMN evidence_model INTEGER NOT NULL DEFAULT 0;
CREATE TRIGGER inventory_evidence_insert BEFORE INSERT ON inventory_entries WHEN (NEW.entry_key LIKE 'device/%' OR NEW.entry_key LIKE 'source/%' OR NEW.entry_key LIKE 'alias/%' OR NEW.entry_key LIKE 'serial/%' OR NEW.entry_key LIKE 'serialconflict/%' OR NEW.entry_key LIKE 'field/%' OR NEW.entry_key LIKE 'indexes/%') AND (SELECT evidence_model FROM inventory_lock WHERE id = 1) = 1 BEGIN SELECT RAISE(ABORT, 'inventory: retired aggregate write'); END;
CREATE TRIGGER inventory_evidence_update BEFORE UPDATE ON inventory_entries WHEN (NEW.entry_key LIKE 'device/%' OR NEW.entry_key LIKE 'source/%' OR NEW.entry_key LIKE 'alias/%' OR NEW.entry_key LIKE 'serial/%' OR NEW.entry_key LIKE 'serialconflict/%' OR NEW.entry_key LIKE 'field/%' OR NEW.entry_key LIKE 'indexes/%') AND (SELECT evidence_model FROM inventory_lock WHERE id = 1) = 1 BEGIN SELECT RAISE(ABORT, 'inventory: retired aggregate write'); END;
-- +down
DROP TRIGGER inventory_evidence_insert;
DROP TRIGGER inventory_evidence_update;
ALTER TABLE inventory_lock DROP COLUMN evidence_model;
