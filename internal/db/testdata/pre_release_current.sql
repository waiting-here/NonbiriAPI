-- Empty-schema fixture only; never executed by application migrations.
DROP INDEX "idx_lake_notes_active_cast_user";
DROP INDEX "idx_lake_notes_cast_retention";
DROP TABLE "lake_notes_casts";
DROP TABLE "lake_notes_profiles";
DROP TABLE "schema_state";
CREATE TABLE lake_notes_casts (
 id TEXT PRIMARY KEY CHECK(length(id)=26 AND substr(id,1,4)='lnc_' AND substr(id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 source_period_id TEXT NOT NULL REFERENCES lake_notes_periods(id) ON DELETE RESTRICT,
 rules_id TEXT NOT NULL CHECK(length(CAST(rules_id AS BLOB)) BETWEEN 1 AND 128),
 phase TEXT NOT NULL CHECK(phase IN ('waiting','playing','success','failed')),
 paused INTEGER NOT NULL CHECK(paused IN (0,1)),
 generation INTEGER NOT NULL CHECK(generation>=1),
 revision INTEGER NOT NULL CHECK(revision>=1),
 last_tick INTEGER NOT NULL CHECK(last_tick BETWEEN 0 AND 9007199254740991),
 snapshot BLOB NOT NULL CHECK(length(snapshot) BETWEEN 1 AND 65536),
 reward_plan BLOB NOT NULL CHECK(length(reward_plan) BETWEEN 1 AND 4096),
 held INTEGER NOT NULL CHECK(held IN (0,1)),
 active_elapsed_ns INTEGER NOT NULL CHECK(active_elapsed_ns>=0),
 active_started_at_ns INTEGER CHECK(active_started_at_ns>=0),
 lease_until_ns INTEGER CHECK(lease_until_ns>=active_started_at_ns),
 last_ack_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(last_ack_json) AND json_type(last_ack_json)='object' AND length(CAST(last_ack_json AS BLOB))<=16384),
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 253402300799),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN created_at AND 253402300799),
 terminal_at INTEGER CHECK(terminal_at BETWEEN created_at AND 253402300799),
 CHECK((paused=1 AND held=0 AND active_started_at_ns IS NULL AND lease_until_ns IS NULL) OR (paused=0 AND active_started_at_ns IS NOT NULL AND lease_until_ns IS NOT NULL)),
 CHECK((phase IN ('waiting','playing') AND terminal_at IS NULL) OR (phase IN ('success','failed') AND terminal_at IS NOT NULL AND paused=1))
) STRICT, WITHOUT ROWID;
CREATE TABLE lake_notes_profiles (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>=1),
 rules_id TEXT NOT NULL CHECK(length(CAST(rules_id AS BLOB)) BETWEEN 1 AND 128),
 coin_mag BLOB NOT NULL CHECK(length(coin_mag)=16),
 profile BLOB NOT NULL CHECK(length(profile) BETWEEN 1 AND 65536),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
CREATE UNIQUE INDEX idx_lake_notes_active_cast_user ON lake_notes_casts(user_id) WHERE phase IN ('waiting','playing');
CREATE INDEX idx_lake_notes_cast_retention ON lake_notes_casts(terminal_at,id) WHERE terminal_at IS NOT NULL;
