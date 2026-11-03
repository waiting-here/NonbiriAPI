package db

// FishingPondHeadsSQL keeps one earliest queue entry per donor. The existing
// donor index supports both the initial backfill and each successor lookup.
const FishingPondHeadsSQL = `CREATE TABLE game_fishing_pond_queue_heads (
 donor_user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 queue_id INTEGER NOT NULL UNIQUE CHECK(queue_id>0)
) STRICT;
INSERT INTO game_fishing_pond_queue_heads(donor_user_id,queue_id)
SELECT donor_user_id,MIN(id) FROM game_fishing_pond_queue GROUP BY donor_user_id;
CREATE TRIGGER fishing_pond_queue_head_insert AFTER INSERT ON game_fishing_pond_queue
BEGIN
 INSERT INTO game_fishing_pond_queue_heads(donor_user_id,queue_id) VALUES(NEW.donor_user_id,NEW.id)
 ON CONFLICT(donor_user_id) DO UPDATE SET queue_id=excluded.queue_id WHERE excluded.queue_id<queue_id;
END;
CREATE TRIGGER fishing_pond_queue_head_delete AFTER DELETE ON game_fishing_pond_queue
WHEN OLD.id=(SELECT queue_id FROM game_fishing_pond_queue_heads WHERE donor_user_id=OLD.donor_user_id)
BEGIN
 UPDATE game_fishing_pond_queue_heads SET queue_id=COALESCE(
  (SELECT id FROM game_fishing_pond_queue WHERE donor_user_id=OLD.donor_user_id ORDER BY id LIMIT 1),queue_id)
 WHERE donor_user_id=OLD.donor_user_id;
 DELETE FROM game_fishing_pond_queue_heads WHERE donor_user_id=OLD.donor_user_id AND queue_id=OLD.id;
END;
`
