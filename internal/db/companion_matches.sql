CREATE TABLE companion_preferences (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 game TEXT NOT NULL CHECK(game IN ('bidding','likes','gwent')),
 memory TEXT NOT NULL CHECK(memory IN ('none','shared','opponent')),
 contribute INTEGER NOT NULL CHECK(contribute IN (0,1)),
 PRIMARY KEY(user_id,game)
) STRICT;
CREATE TABLE companion_matches (
 id TEXT PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 game TEXT NOT NULL CHECK(game IN ('bidding','likes','gwent')),
 mode TEXT NOT NULL CHECK(mode IN ('standard','quick')),
 program TEXT NOT NULL,
 program_id INTEGER NOT NULL CHECK(program_id>=0),
 version INTEGER NOT NULL,
 difficulty INTEGER NOT NULL,
 official INTEGER NOT NULL CHECK(official IN (0,1)),
 seat INTEGER NOT NULL CHECK(seat IN (0,1)),
 rules_hash TEXT NOT NULL,
 memory TEXT NOT NULL CHECK(memory IN ('none','shared','opponent')),
 contribute INTEGER NOT NULL CHECK(contribute IN (0,1)),
 personal_skipped INTEGER NOT NULL DEFAULT 0,
 global_skipped INTEGER NOT NULL DEFAULT 0,
 start_key TEXT NOT NULL,
 start_hash TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 ended_at INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL CHECK(status IN ('active','completed','abandoned','cancelled','technical')),
 reason TEXT NOT NULL DEFAULT '',
 winner INTEGER CHECK(winner IN (0,1)),
 score0 INTEGER NOT NULL DEFAULT 0,
 score1 INTEGER NOT NULL DEFAULT 0,
 ai_state TEXT NOT NULL CHECK(ai_state IN ('idle','queued','thinking','locked')),
 queued_at INTEGER NOT NULL DEFAULT 0,
 queue_ms INTEGER NOT NULL DEFAULT 0,
 execution_ms INTEGER NOT NULL DEFAULT 0,
 position TEXT NOT NULL,
 record TEXT NOT NULL,
 record_bytes INTEGER GENERATED ALWAYS AS (length(CAST(record AS BLOB))) STORED,
 UNIQUE(user_id,game,start_key)
) STRICT;
CREATE UNIQUE INDEX companion_active_owner ON companion_matches(user_id,game) WHERE status='active';
CREATE INDEX companion_pending ON companion_matches(queued_at,id) WHERE status='active' AND ai_state='queued';
CREATE INDEX companion_startup ON companion_matches(game,id) WHERE status='active';
CREATE INDEX companion_history ON companion_matches(user_id,game,ended_at DESC,id DESC) WHERE ended_at>0;
CREATE INDEX companion_expiry ON companion_matches(game,ended_at,id) WHERE ended_at>0;
CREATE INDEX companion_personal ON companion_matches(user_id,game,ended_at DESC,id DESC) WHERE status='completed';
CREATE INDEX companion_opponent ON companion_matches(user_id,game,program_id,difficulty,ended_at DESC,id DESC) WHERE status='completed';
CREATE INDEX companion_global ON companion_matches(game,mode,rules_hash,ended_at DESC,id DESC) WHERE status='completed' AND official=1 AND contribute=1;
CREATE TABLE companion_history_snapshot (
 match_id TEXT NOT NULL REFERENCES companion_matches(id) ON DELETE CASCADE,
 record_id TEXT NOT NULL REFERENCES companion_matches(id) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL,
 personal INTEGER NOT NULL CHECK(personal IN (0,1)),
 global_source INTEGER NOT NULL CHECK(global_source IN (0,1)),
 bytes INTEGER NOT NULL CHECK(bytes>0),
 PRIMARY KEY(match_id,ordinal),
 UNIQUE(match_id,record_id)
) STRICT;
CREATE INDEX companion_snapshot_record ON companion_history_snapshot(record_id);
