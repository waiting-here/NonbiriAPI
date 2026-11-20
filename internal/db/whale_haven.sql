CREATE TABLE whale_haven_settings (
 revision INTEGER PRIMARY KEY CHECK(revision>=2), effective_at INTEGER NOT NULL CHECK(effective_at>=0),
 document TEXT NOT NULL CHECK(json_valid(document) AND json_type(document)='object')
) STRICT;
CREATE INDEX idx_whale_settings_time ON whale_haven_settings(effective_at,revision);
CREATE TABLE whale_haven_master_history (
 effective_at INTEGER PRIMARY KEY CHECK(effective_at>=0), enabled INTEGER NOT NULL CHECK(enabled IN (0,1))
) STRICT;
INSERT INTO whale_haven_master_history VALUES (0,COALESCE((SELECT value='1' FROM site_config WHERE key='games_enabled'),0));
CREATE TRIGGER whale_master_insert AFTER INSERT ON site_config WHEN NEW.key='games_enabled' AND NOT EXISTS (SELECT 1 FROM whale_haven_master_history WHERE effective_at BETWEEN NEW.updated_at*1000 AND NEW.updated_at*1000+999 AND enabled=(NEW.value='1') AND effective_at=(SELECT MAX(effective_at) FROM whale_haven_master_history))
BEGIN
 INSERT INTO whale_haven_master_history VALUES(NEW.updated_at*1000,NEW.value='1')
 ON CONFLICT(effective_at) DO UPDATE SET enabled=excluded.enabled;
END;
CREATE TRIGGER whale_master_update AFTER UPDATE OF value ON site_config
WHEN NEW.key='games_enabled' AND NEW.value<>OLD.value AND NOT EXISTS (SELECT 1 FROM whale_haven_master_history WHERE effective_at BETWEEN NEW.updated_at*1000 AND NEW.updated_at*1000+999 AND enabled=(NEW.value='1') AND effective_at=(SELECT MAX(effective_at) FROM whale_haven_master_history))
BEGIN
 INSERT INTO whale_haven_master_history VALUES(NEW.updated_at*1000,NEW.value='1')
 ON CONFLICT(effective_at) DO UPDATE SET enabled=excluded.enabled;
END;
CREATE TABLE whale_haven_profiles (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 door TEXT NOT NULL UNIQUE, is_open INTEGER NOT NULL CHECK(is_open IN(0,1)),
 whale_name TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 snapshot TEXT NOT NULL CHECK(json_valid(snapshot) AND json_type(snapshot)='object'), updated_at INTEGER NOT NULL CHECK(updated_at>=0)
) STRICT;
CREATE INDEX idx_whale_open_door ON whale_haven_profiles(door) WHERE is_open=1;
CREATE TABLE whale_haven_visits (
 guest_id INTEGER PRIMARY KEY REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 host_id INTEGER NOT NULL REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 started_at INTEGER NOT NULL, allow_hire INTEGER NOT NULL CHECK(allow_hire IN (0,1)),
 working INTEGER NOT NULL CHECK(working IN (0,1)), work_left_ms INTEGER NOT NULL CHECK(work_left_ms>=0),
 eaten_grams REAL NOT NULL CHECK(eaten_grams>=0), silver_eaten_grams REAL NOT NULL CHECK(silver_eaten_grams>=0), CHECK(guest_id<>host_id)
) STRICT;
CREATE INDEX idx_whale_visits_host ON whale_haven_visits(host_id,guest_id);
CREATE TABLE whale_haven_wage_fractions (
 guest_id INTEGER PRIMARY KEY REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 microcoins REAL NOT NULL CHECK(microcoins>=0)
) STRICT, WITHOUT ROWID;
CREATE TABLE whale_haven_daily (
 user_id INTEGER NOT NULL REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 site_day TEXT NOT NULL, exchanged INTEGER NOT NULL DEFAULT 0 CHECK(exchanged>=0),
 social INTEGER NOT NULL DEFAULT 0 CHECK(social>=0), shards INTEGER NOT NULL DEFAULT 0 CHECK(shards>=0),
 PRIMARY KEY(user_id,site_day)
) STRICT, WITHOUT ROWID;
CREATE TABLE whale_haven_pins (
 owner_id INTEGER NOT NULL REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 peer_id INTEGER NOT NULL REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 PRIMARY KEY(owner_id,peer_id), CHECK(owner_id<>peer_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE whale_haven_events (
 id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 peer_id INTEGER REFERENCES whale_haven_profiles(user_id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL,
 pearls INTEGER NOT NULL DEFAULT 0, shards INTEGER NOT NULL DEFAULT 0, operation_id TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX idx_whale_events_user ON whale_haven_events(user_id,id DESC);
CREATE INDEX idx_whale_events_peer ON whale_haven_events(peer_id) WHERE peer_id IS NOT NULL;
CREATE TABLE whale_haven_receipts (
 user_id INTEGER NOT NULL REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 key_hash BLOB NOT NULL CHECK(length(key_hash)=32), request_hash BLOB NOT NULL CHECK(length(request_hash)=32),
 operation_id TEXT NOT NULL, created_at INTEGER NOT NULL,
 PRIMARY KEY(user_id,key_hash)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_whale_receipts_time ON whale_haven_receipts(user_id,created_at);
CREATE TABLE whale_haven_assets (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 pearls INTEGER NOT NULL CHECK(pearls BETWEEN 0 AND 9000000000000),
 shards INTEGER NOT NULL CHECK(shards BETWEEN 0 AND 9000000000000)
) STRICT;
CREATE TABLE whale_haven_movements (
 id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 created_at INTEGER NOT NULL, reason TEXT NOT NULL, operation_id TEXT NOT NULL,
 pearls_delta INTEGER NOT NULL, shards_delta INTEGER NOT NULL, pearls_after INTEGER NOT NULL, shards_after INTEGER NOT NULL,
 CHECK(pearls_delta<>0 OR shards_delta<>0)
) STRICT;
CREATE INDEX idx_whale_movements_user ON whale_haven_movements(user_id,id DESC);
CREATE TABLE whale_haven_footprints (
 user_id INTEGER NOT NULL REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 peer_id INTEGER NOT NULL REFERENCES whale_haven_profiles(user_id) ON DELETE CASCADE,
 outgoing INTEGER NOT NULL, incoming INTEGER NOT NULL, last_at INTEGER NOT NULL, last_summary TEXT NOT NULL,
 PRIMARY KEY(user_id,peer_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_whale_footprints_recent ON whale_haven_footprints(user_id,last_at DESC);
CREATE TRIGGER whale_record_footprint AFTER INSERT ON whale_haven_events WHEN NEW.peer_id IS NOT NULL
BEGIN
 INSERT INTO whale_haven_footprints VALUES(NEW.user_id,NEW.peer_id,NEW.kind='visit_out',NEW.kind='guest_in',NEW.created_at,NEW.text)
 ON CONFLICT(user_id,peer_id) DO UPDATE SET outgoing=outgoing+excluded.outgoing,incoming=incoming+excluded.incoming,
 last_at=max(last_at,excluded.last_at),last_summary=CASE WHEN excluded.last_at>=last_at THEN excluded.last_summary ELSE last_summary END;
END;
