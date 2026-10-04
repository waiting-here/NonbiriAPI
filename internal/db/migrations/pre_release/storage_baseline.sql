CREATE TABLE schema_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 version INTEGER NOT NULL CHECK(version>=1)
) STRICT;
ALTER TABLE lake_notes_profiles ADD COLUMN storage_version INTEGER NOT NULL DEFAULT 1 CHECK(storage_version>=1);
ALTER TABLE lake_notes_casts ADD COLUMN storage_version INTEGER NOT NULL DEFAULT 1 CHECK(storage_version>=1);
INSERT INTO schema_state(id,version) VALUES(1,1);
