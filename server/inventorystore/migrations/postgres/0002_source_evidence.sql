-- +up
ALTER TABLE inventory_lock ADD COLUMN evidence_model INTEGER NOT NULL DEFAULT 0;
CREATE FUNCTION inventory_evidence_guard() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF (NEW.entry_key LIKE 'device/%' OR NEW.entry_key LIKE 'source/%' OR NEW.entry_key LIKE 'alias/%' OR NEW.entry_key LIKE 'serial/%' OR NEW.entry_key LIKE 'serialconflict/%' OR NEW.entry_key LIKE 'field/%' OR NEW.entry_key LIKE 'indexes/%') AND (SELECT evidence_model FROM inventory_lock WHERE id = 1) = 1 THEN RAISE EXCEPTION 'inventory: retired aggregate write'; END IF; RETURN NEW; END $$;
CREATE TRIGGER inventory_evidence_write BEFORE INSERT OR UPDATE ON inventory_entries FOR EACH ROW EXECUTE FUNCTION inventory_evidence_guard();
-- +down
DROP TRIGGER inventory_evidence_write ON inventory_entries;
DROP FUNCTION inventory_evidence_guard();
ALTER TABLE inventory_lock DROP COLUMN evidence_model;
