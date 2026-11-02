package db

// FishingRecentOrderSQL keeps the existing recent-length ranking order in an
// indexed projection. Its backfill copies active facts without changing them.
const FishingRecentOrderSQL = `CREATE TABLE game_fishing_recent_order (
 batch_id_text TEXT PRIMARY KEY NOT NULL REFERENCES game_fishing_length_facts(batch_id_text) ON DELETE CASCADE,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at INTEGER NOT NULL CHECK(expires_at BETWEEN 0 AND 253402300799),
 length_cm TEXT NOT NULL CHECK(length(length_cm) BETWEEN 1 AND 128
  AND length(CAST(length_cm AS BLOB))=length(length_cm)
  AND length_cm NOT GLOB '*[^0-9]*' AND (length_cm='0' OR substr(length_cm,1,1)<>'0')),
 caught_at INTEGER NOT NULL CHECK(caught_at BETWEEN 0 AND 253402300799),
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 9)
) STRICT;
CREATE TRIGGER fishing_recent_order_insert_guard BEFORE INSERT ON game_fishing_recent_order
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_length_facts l
 JOIN game_fishing_rank_facts f ON f.batch_id_text=l.batch_id_text
 WHERE l.batch_id_text=NEW.batch_id_text AND f.aggregate_applied=1
 AND NEW.user_id=f.user_id AND NEW.expires_at=f.expires_at
 AND NEW.length_cm=COALESCE(l.blue_fat_fish_length_cm,l.milk_crab_length_cm,CAST(l.size_cm AS TEXT))
 AND NEW.caught_at=l.caught_at AND NEW.ordinal=l.ordinal)
BEGIN SELECT RAISE(ABORT,'fishing recent order does not match length fact'); END;
CREATE TRIGGER fishing_recent_order_update_guard BEFORE UPDATE ON game_fishing_recent_order
BEGIN SELECT RAISE(ABORT,'fishing recent order is immutable'); END;
CREATE TRIGGER fishing_length_fact_recent_order AFTER INSERT ON game_fishing_length_facts
BEGIN
 INSERT INTO game_fishing_recent_order(batch_id_text,user_id,expires_at,length_cm,caught_at,ordinal)
 SELECT NEW.batch_id_text,f.user_id,f.expires_at,
  COALESCE(NEW.blue_fat_fish_length_cm,NEW.milk_crab_length_cm,CAST(NEW.size_cm AS TEXT)),NEW.caught_at,NEW.ordinal
 FROM game_fishing_rank_facts f WHERE f.batch_id_text=NEW.batch_id_text AND f.aggregate_applied=1;
END;
INSERT INTO game_fishing_recent_order(batch_id_text,user_id,expires_at,length_cm,caught_at,ordinal)
 SELECT l.batch_id_text,f.user_id,f.expires_at,
  COALESCE(l.blue_fat_fish_length_cm,l.milk_crab_length_cm,CAST(l.size_cm AS TEXT)),l.caught_at,l.ordinal
 FROM game_fishing_length_facts l JOIN game_fishing_rank_facts f ON f.batch_id_text=l.batch_id_text
 WHERE f.aggregate_applied=1;
CREATE INDEX idx_fishing_recent_order ON game_fishing_recent_order(user_id,length(length_cm) DESC,length_cm COLLATE BINARY DESC,caught_at,batch_id_text,ordinal,expires_at);
`
