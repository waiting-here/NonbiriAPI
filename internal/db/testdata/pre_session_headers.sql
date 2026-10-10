DROP TABLE charity_header_uses;
DROP TABLE charity_header_sessions;
ALTER TABLE charity_models DROP COLUMN session_headers;
UPDATE schema_state SET version=11 WHERE id=1;
