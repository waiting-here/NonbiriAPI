package db

import (
	"context"
	"database/sql"
)

// Presentation lengths do not alter the original size or economic outcome.
// The rolling snapshot follows rank-fact retention independently of ACK and
// outcome cleanup; the lifetime snapshot follows the user's existing best.
const fishingLengthSchema = `
CREATE TABLE game_fishing_outcome_lengths (
 batch_id TEXT NOT NULL,
 ordinal INTEGER NOT NULL CHECK(typeof(ordinal)='integer' AND ordinal BETWEEN 0 AND 9),
 length_cm TEXT NOT NULL CHECK(typeof(length_cm)='text' AND length(length_cm) BETWEEN 3 AND 128
  AND length(CAST(length_cm AS BLOB))=length(length_cm)
  AND substr(length_cm,1,1)<>'0' AND length_cm NOT GLOB '*[^0-9]*'
  AND (length(length_cm)>3 OR length_cm>='201')),
 PRIMARY KEY(batch_id,ordinal),
 FOREIGN KEY(batch_id,ordinal) REFERENCES game_fishing_outcomes(batch_id,ordinal) ON DELETE CASCADE
);
CREATE TRIGGER fishing_outcome_length_insert_guard BEFORE INSERT ON game_fishing_outcome_lengths
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_outcomes o JOIN game_fishing_batches b ON b.id=o.batch_id
 WHERE o.batch_id=NEW.batch_id AND o.ordinal=NEW.ordinal AND o.tier='legend' AND b.state='reserved')
BEGIN SELECT RAISE(ABORT,'fishing presentation requires a reserved legend'); END;
CREATE TRIGGER fishing_outcome_length_update_guard BEFORE UPDATE ON game_fishing_outcome_lengths
BEGIN SELECT RAISE(ABORT,'fishing presentation is immutable'); END;
CREATE TRIGGER fishing_outcome_length_delete_guard BEFORE DELETE ON game_fishing_outcome_lengths
WHEN EXISTS(SELECT 1 FROM game_fishing_outcomes o JOIN game_fishing_batches b ON b.id=o.batch_id
 WHERE o.batch_id=OLD.batch_id AND o.ordinal=OLD.ordinal AND b.state<>'reserved')
BEGIN SELECT RAISE(ABORT,'terminal fishing presentation is immutable'); END;
CREATE TRIGGER fishing_presented_outcome_update_guard BEFORE UPDATE ON game_fishing_outcomes
WHEN EXISTS(SELECT 1 FROM game_fishing_outcome_lengths l WHERE l.batch_id=OLD.batch_id AND l.ordinal=OLD.ordinal)
BEGIN SELECT RAISE(ABORT,'presented fishing outcome is immutable'); END;

CREATE TABLE game_fishing_best_lengths (
 user_id INTEGER PRIMARY KEY REFERENCES game_fishing_best(user_id) ON DELETE CASCADE,
 length_cm TEXT NOT NULL CHECK(typeof(length_cm)='text' AND length(length_cm) BETWEEN 3 AND 128
  AND length(CAST(length_cm AS BLOB))=length(length_cm)
  AND substr(length_cm,1,1)<>'0' AND length_cm NOT GLOB '*[^0-9]*'
  AND (length(length_cm)>3 OR length_cm>='201'))
);
CREATE INDEX idx_fishing_best_lengths_rank ON game_fishing_best_lengths(length(length_cm) DESC,length_cm DESC,user_id);
CREATE TRIGGER fishing_best_length_insert_guard BEFORE INSERT ON game_fishing_best_lengths
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_best b JOIN game_fishing_outcome_lengths l
 ON l.batch_id=b.batch_id AND l.ordinal=b.ordinal
 WHERE b.user_id=NEW.user_id AND b.tier='legend' AND l.length_cm=NEW.length_cm)
BEGIN SELECT RAISE(ABORT,'fishing best presentation mismatch'); END;
CREATE TRIGGER fishing_best_length_update_guard BEFORE UPDATE ON game_fishing_best_lengths
WHEN NEW.user_id IS NOT OLD.user_id OR NOT EXISTS(SELECT 1 FROM game_fishing_best b
 JOIN game_fishing_outcome_lengths l ON l.batch_id=b.batch_id AND l.ordinal=b.ordinal
 WHERE b.user_id=NEW.user_id AND b.tier='legend' AND l.length_cm=NEW.length_cm)
BEGIN SELECT RAISE(ABORT,'fishing best presentation mismatch'); END;
CREATE TRIGGER fishing_presented_best_update_guard BEFORE UPDATE ON game_fishing_best
WHEN EXISTS(SELECT 1 FROM game_fishing_best_lengths l WHERE l.user_id=OLD.user_id)
 AND NOT (
 (NEW.user_id IS OLD.user_id AND NEW.species_key IS OLD.species_key AND NEW.tier IS OLD.tier
  AND NEW.size_cm IS OLD.size_cm AND NEW.caught_at IS OLD.caught_at AND NEW.public_tie_key IS OLD.public_tie_key
  AND NEW.batch_id IS NULL AND NEW.ordinal IS NULL AND OLD.batch_id IS NOT NULL
  AND NOT EXISTS(SELECT 1 FROM game_fishing_outcomes o WHERE o.batch_id=OLD.batch_id AND o.ordinal=OLD.ordinal))
 OR (NEW.user_id IS OLD.user_id AND NEW.tier='legend' AND EXISTS(
  SELECT 1 FROM game_fishing_best_lengths previous JOIN game_fishing_outcome_lengths fresh
  ON fresh.batch_id=NEW.batch_id AND fresh.ordinal=NEW.ordinal
  WHERE previous.user_id=OLD.user_id AND previous.length_cm=fresh.length_cm)))
BEGIN SELECT RAISE(ABORT,'fishing best presentation mismatch'); END;

CREATE TABLE game_fishing_length_facts (
 batch_id_text TEXT PRIMARY KEY NOT NULL REFERENCES game_fishing_rank_facts(batch_id_text) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL CHECK(typeof(ordinal)='integer' AND ordinal BETWEEN 0 AND 9),
 species_key TEXT NOT NULL,
 tier TEXT NOT NULL CHECK(tier IN ('junk','small','regular','big','giant','legend','treasure')),
 size_cm INTEGER NOT NULL CHECK(typeof(size_cm)='integer' AND size_cm BETWEEN 0 AND 200),
 caught_at INTEGER NOT NULL CHECK(typeof(caught_at)='integer' AND caught_at BETWEEN 0 AND 253402300799),
 blue_fat_fish_length_cm TEXT CHECK(blue_fat_fish_length_cm IS NULL OR
  (tier='legend' AND typeof(blue_fat_fish_length_cm)='text' AND length(blue_fat_fish_length_cm) BETWEEN 3 AND 128
   AND length(CAST(blue_fat_fish_length_cm AS BLOB))=length(blue_fat_fish_length_cm)
   AND substr(blue_fat_fish_length_cm,1,1)<>'0' AND blue_fat_fish_length_cm NOT GLOB '*[^0-9]*'
   AND (length(blue_fat_fish_length_cm)>3 OR blue_fat_fish_length_cm>='201')))
);
CREATE TRIGGER fishing_length_fact_insert_guard BEFORE INSERT ON game_fishing_length_facts
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_rank_facts f
 JOIN game_fishing_batches b ON b.id=f.batch_id_text AND b.user_id=f.user_id
 JOIN game_fishing_outcomes o ON o.batch_id=b.id AND o.ordinal=NEW.ordinal
 LEFT JOIN game_fishing_outcome_lengths l ON l.batch_id=o.batch_id AND l.ordinal=o.ordinal
 WHERE f.batch_id_text=NEW.batch_id_text AND f.aggregate_applied=1 AND b.state IN ('reserved','committed')
 AND NEW.species_key=o.species_key AND NEW.tier=o.tier AND NEW.size_cm=o.size_cm
 AND NEW.caught_at=b.created_at AND NEW.blue_fat_fish_length_cm IS l.length_cm
 AND (SELECT count(*) FROM game_fishing_outcomes all_o WHERE all_o.batch_id=b.id)=b.count
 AND NEW.ordinal=(SELECT all_o.ordinal FROM game_fishing_outcomes all_o
  LEFT JOIN game_fishing_outcome_lengths all_l ON all_l.batch_id=all_o.batch_id AND all_l.ordinal=all_o.ordinal
  WHERE all_o.batch_id=b.id ORDER BY length(COALESCE(all_l.length_cm,CAST(all_o.size_cm AS TEXT))) DESC,
  COALESCE(all_l.length_cm,CAST(all_o.size_cm AS TEXT)) COLLATE BINARY DESC,all_o.ordinal ASC LIMIT 1))
BEGIN SELECT RAISE(ABORT,'fishing length fact does not match batch maximum'); END;
CREATE TRIGGER fishing_length_fact_update_guard BEFORE UPDATE ON game_fishing_length_facts
BEGIN SELECT RAISE(ABORT,'fishing length fact is immutable'); END;
`

// Backfill only retained complete settlements with their original rank fact.
// No unknown historical catches, lifetime bests or economic values are changed.
func migrateFishingLengthFacts(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO game_fishing_length_facts
 (batch_id_text,ordinal,species_key,tier,size_cm,caught_at,blue_fat_fish_length_cm)
 SELECT b.id,o.ordinal,o.species_key,o.tier,o.size_cm,b.created_at,NULL
 FROM game_fishing_batches b JOIN game_fishing_rank_facts f ON f.batch_id_text=b.id AND f.user_id=b.user_id
 JOIN game_fishing_outcomes o ON o.batch_id=b.id
 WHERE b.state='committed' AND b.settled_at=f.settled_at AND f.aggregate_applied=1
 AND (SELECT count(*) FROM game_fishing_outcomes all_o WHERE all_o.batch_id=b.id)=b.count
 AND o.ordinal=(SELECT all_o.ordinal FROM game_fishing_outcomes all_o WHERE all_o.batch_id=b.id
  ORDER BY all_o.size_cm DESC,all_o.ordinal ASC LIMIT 1)`)
	return err
}
