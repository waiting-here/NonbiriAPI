package db

// FishingPondSQL adds only new-catch eligibility. Queued gifts and cumulative
// counts intentionally outlive the original fishing batch retention window.
const FishingPondSQL = `CREATE TABLE game_fishing_pond_outcomes (
 batch_id TEXT NOT NULL,
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 9),
 returned_at INTEGER CHECK(returned_at BETWEEN 0 AND 253402300799),
 gift_species_key TEXT CHECK(gift_species_key IN ('boot','seaweed','plastic_bag','branch','old_tire','glasses','phone_case')),
 donor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 donor_display_name TEXT CHECK(length(CAST(donor_display_name AS BLOB)) BETWEEN 1 AND 512),
 donor_avatar_url TEXT CHECK(length(CAST(donor_avatar_url AS BLOB)) BETWEEN 1 AND 2048),
 PRIMARY KEY(batch_id,ordinal),
 FOREIGN KEY(batch_id,ordinal) REFERENCES game_fishing_outcomes(batch_id,ordinal) ON DELETE CASCADE,
 CHECK((gift_species_key IS NULL AND donor_user_id IS NULL AND donor_display_name IS NULL AND donor_avatar_url IS NULL)
  OR (gift_species_key IS NOT NULL AND returned_at IS NULL
   AND ((donor_user_id IS NULL AND donor_display_name IS NULL AND donor_avatar_url IS NULL)
    OR (donor_user_id IS NOT NULL AND donor_display_name IS NOT NULL))))
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_fishing_pond_outcomes_donor ON game_fishing_pond_outcomes(donor_user_id,batch_id,ordinal) WHERE donor_user_id IS NOT NULL;
CREATE TRIGGER fishing_pond_outcome_insert_guard BEFORE INSERT ON game_fishing_pond_outcomes
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_outcomes o JOIN game_fishing_batches b ON b.id=o.batch_id
 WHERE o.batch_id=NEW.batch_id AND o.ordinal=NEW.ordinal AND b.state='reserved' AND o.tier='junk' AND o.payout_milli=0)
 OR NEW.returned_at IS NOT NULL
BEGIN SELECT RAISE(ABORT,'fishing pond eligibility requires a new zero-value catch'); END;
CREATE TRIGGER fishing_pond_outcome_update_guard BEFORE UPDATE ON game_fishing_pond_outcomes
WHEN NEW.batch_id IS NOT OLD.batch_id OR NEW.ordinal IS NOT OLD.ordinal
 OR (NEW.gift_species_key IS NOT OLD.gift_species_key AND NOT(
  OLD.gift_species_key IS NULL AND NEW.gift_species_key IS NOT NULL AND OLD.returned_at IS NULL AND NEW.returned_at IS NULL
  AND EXISTS(SELECT 1 FROM game_fishing_batches WHERE id=OLD.batch_id AND state='reserved')))
 OR (OLD.returned_at IS NOT NULL AND NEW.returned_at IS NOT OLD.returned_at)
 OR ((NEW.donor_user_id IS NOT OLD.donor_user_id OR NEW.donor_display_name IS NOT OLD.donor_display_name OR NEW.donor_avatar_url IS NOT OLD.donor_avatar_url)
  AND NOT((NEW.donor_user_id IS NULL AND NEW.donor_display_name IS NULL AND NEW.donor_avatar_url IS NULL)
   OR (OLD.gift_species_key IS NULL AND NEW.gift_species_key IS NOT NULL AND OLD.returned_at IS NULL AND NEW.returned_at IS NULL
    AND EXISTS(SELECT 1 FROM game_fishing_batches WHERE id=OLD.batch_id AND state='reserved'))))
BEGIN SELECT RAISE(ABORT,'fishing pond catch identity is immutable'); END;
CREATE TABLE game_fishing_pond_queue (
 id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(id>0),
 donor_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 source_batch_id TEXT NOT NULL CHECK(length(source_batch_id)=25 AND substr(source_batch_id,1,3)='fb_' AND substr(source_batch_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_batch_id,-1,1) IN ('A','Q','g','w')),
 source_ordinal INTEGER NOT NULL CHECK(source_ordinal BETWEEN 0 AND 9),
 species_key TEXT NOT NULL CHECK(species_key IN ('boot','seaweed','plastic_bag','branch','old_tire','glasses','phone_case')),
 donor_display_name TEXT NOT NULL CHECK(length(CAST(donor_display_name AS BLOB)) BETWEEN 1 AND 512),
 donor_avatar_url TEXT CHECK(length(CAST(donor_avatar_url AS BLOB)) BETWEEN 1 AND 2048),
 returned_at INTEGER NOT NULL CHECK(returned_at BETWEEN 0 AND 253402300799),
 UNIQUE(source_batch_id,source_ordinal)
) STRICT;
CREATE INDEX idx_fishing_pond_queue_donor ON game_fishing_pond_queue(donor_user_id,id);
CREATE TRIGGER fishing_pond_queue_update_guard BEFORE UPDATE ON game_fishing_pond_queue
BEGIN SELECT RAISE(ABORT,'queued fishing gifts are immutable'); END;
CREATE TABLE game_fishing_pond_queue_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 item_count INTEGER NOT NULL CHECK(item_count>=0)
) STRICT;
INSERT INTO game_fishing_pond_queue_state VALUES(1,0);
CREATE TRIGGER fishing_pond_queue_count_insert AFTER INSERT ON game_fishing_pond_queue
BEGIN
 UPDATE game_fishing_pond_queue_state SET item_count=item_count+1 WHERE id=1;
 SELECT CASE WHEN changes()<>1 THEN RAISE(ABORT,'fishing pond queue state is missing') END;
END;
CREATE TRIGGER fishing_pond_queue_count_delete AFTER DELETE ON game_fishing_pond_queue
BEGIN
 UPDATE game_fishing_pond_queue_state SET item_count=item_count-1 WHERE id=1;
 SELECT CASE WHEN changes()<>1 THEN RAISE(ABORT,'fishing pond queue state is missing') END;
END;
CREATE TABLE game_fishing_pond_counts (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 board TEXT NOT NULL CHECK(board IN ('cleanup','gifts')),
 occurred_at INTEGER NOT NULL CHECK(occurred_at BETWEEN 0 AND 253402300799),
 item_count INTEGER NOT NULL CHECK(item_count>0),
 PRIMARY KEY(user_id,board,occurred_at)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_fishing_pond_counts_expiry ON game_fishing_pond_counts(occurred_at,user_id,board);
CREATE INDEX idx_fishing_pond_counts_board ON game_fishing_pond_counts(board,occurred_at,user_id,item_count);
CREATE TABLE game_fishing_pond_daily (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 board TEXT NOT NULL CHECK(board IN ('cleanup','gifts')),
 day_start INTEGER NOT NULL CHECK(day_start BETWEEN 0 AND 253402214400 AND day_start%86400=0),
 item_count INTEGER NOT NULL CHECK(item_count>0),
 last_occurred_at INTEGER NOT NULL CHECK(last_occurred_at BETWEEN day_start AND day_start+86399 AND last_occurred_at<=253402300799),
 PRIMARY KEY(user_id,board,day_start)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_fishing_pond_daily_board ON game_fishing_pond_daily(board,day_start,user_id,item_count,last_occurred_at);
CREATE INDEX idx_fishing_pond_daily_expiry ON game_fishing_pond_daily(day_start,user_id,board);
CREATE TABLE game_fishing_pond_totals (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 board TEXT NOT NULL CHECK(board IN ('cleanup','gifts')),
 item_count INTEGER NOT NULL CHECK(item_count>0),
 achieved_at INTEGER NOT NULL CHECK(achieved_at BETWEEN 0 AND 253402300799),
 public_tie_key BLOB NOT NULL CHECK(length(public_tie_key)=32),
 PRIMARY KEY(user_id,board)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_fishing_pond_totals_rank ON game_fishing_pond_totals(board,item_count DESC,achieved_at,public_tie_key);
`
