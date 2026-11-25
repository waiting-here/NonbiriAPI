package db

// CompanionProgramsSQL is shared by fresh initialization and the offline converter.
const CompanionProgramsSQL = `
CREATE TABLE companion_programs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
 game TEXT NOT NULL CHECK(game IN ('bidding','likes','gwent')),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 active_version INTEGER NOT NULL DEFAULT 0 CHECK(active_version>=0),
 last_version INTEGER NOT NULL DEFAULT 0 CHECK(last_version>=active_version),
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 CHECK((user_id IS NULL AND name='official') OR (user_id IS NOT NULL AND name<>'official'))
) STRICT;
CREATE UNIQUE INDEX companion_private_names ON companion_programs(user_id,game,name) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX companion_official_game ON companion_programs(game) WHERE user_id IS NULL;
CREATE TABLE companion_program_versions (
 program_id INTEGER NOT NULL REFERENCES companion_programs(id) ON DELETE CASCADE,
 version INTEGER NOT NULL CHECK(version>0),
 source TEXT NOT NULL,
 module BLOB,
 diagnostic TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 PRIMARY KEY(program_id,version)
) STRICT;
CREATE INDEX companion_program_expiry ON companion_program_versions(created_at,program_id,version);
`
