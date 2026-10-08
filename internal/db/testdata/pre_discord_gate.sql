ALTER TABLE users DROP COLUMN discord_gate_policy;
UPDATE schema_state SET version=5 WHERE id=1;
