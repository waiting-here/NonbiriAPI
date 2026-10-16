package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// migrateFishingPresentations preserves accepted outcomes and their original
// probabilities. Existing sidecars become blue; accepted batches get milk=0.
func migrateFishingPresentations(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_state WHERE id=1`).Scan(&version); err != nil {
		return err
	}
	if version != 17 {
		return errors.New("fishing presentation migration requires schema 17")
	}
	if err := rebuildTables(ctx, tx, []tableRebuild{
		{Name: "game_fishing_batches", SQL: `CREATE TABLE game_fishing_batches (id TEXT NOT NULL PRIMARY KEY CHECK(length(id)=25 AND substr(id,1,3)='fb_' AND substr(id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,-1,1) IN ('A','Q','g','w')), user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, bait TEXT NOT NULL CHECK(bait IN ('worm','lure','premium')), count INTEGER NOT NULL CHECK(count IN (1,10)), unit_price_milli INTEGER NOT NULL CHECK(unit_price_milli BETWEEN 0 AND 9000000000000000), entry_total_milli INTEGER NOT NULL CHECK(entry_total_milli BETWEEN 0 AND 9000000000000000), payout_total_milli INTEGER NOT NULL CHECK(payout_total_milli BETWEEN 0 AND 9000000000000000), operation_id TEXT NOT NULL UNIQUE CHECK(length(operation_id)=25 AND substr(operation_id,1,3)='op_' AND substr(operation_id,4) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(operation_id,-1,1) IN ('A','Q','g','w')), request_hash BLOB NOT NULL CHECK(typeof(request_hash)='blob' AND length(request_hash)=32), state TEXT NOT NULL CHECK(state IN ('reserved','committed','released')), ledger_rows_remaining BLOB NOT NULL CHECK(typeof(ledger_rows_remaining)='blob' AND length(ledger_rows_remaining)=16), attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 10), next_attempt_at INTEGER, last_error_class TEXT CHECK(last_error_class IS NULL OR last_error_class IN ('rng_failed','settlement_failed','db_busy','internal_retryable','invariant_violation')), retry_exhausted INTEGER NOT NULL DEFAULT 0 CHECK(retry_exhausted IN (0,1)), created_at INTEGER NOT NULL, settled_at INTEGER, revealed_at INTEGER, rules_version INTEGER NOT NULL DEFAULT 1 CHECK(typeof(rules_version)='integer' AND rules_version BETWEEN 1 AND 2), game_paid_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(game_paid_milli)='integer' AND game_paid_milli BETWEEN 0 AND entry_total_milli), platform_bp INTEGER NOT NULL DEFAULT 0 CHECK(typeof(platform_bp)='integer' AND platform_bp BETWEEN 0 AND 9999), welfare_bp INTEGER NOT NULL DEFAULT 0 CHECK(typeof(welfare_bp)='integer' AND welfare_bp BETWEEN 0 AND 9999), thursday_bp INTEGER NOT NULL DEFAULT 0 CHECK(typeof(thursday_bp)='integer' AND thursday_bp BETWEEN 0 AND 9999), net_payout_total_milli INTEGER CHECK(net_payout_total_milli IS NULL OR (typeof(net_payout_total_milli)='integer' AND net_payout_total_milli BETWEEN 0 AND 9000000000000000)), platform_cut_total_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(platform_cut_total_milli)='integer' AND platform_cut_total_milli BETWEEN 0 AND 9000000000000000), welfare_cut_total_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(welfare_cut_total_milli)='integer' AND welfare_cut_total_milli BETWEEN 0 AND 9000000000000000), thursday_cut_total_milli INTEGER NOT NULL DEFAULT 0 CHECK(typeof(thursday_cut_total_milli)='integer' AND thursday_cut_total_milli BETWEEN 0 AND 9000000000000000), blue_fish_chance_bps INTEGER NOT NULL DEFAULT 1000
 CHECK(typeof(blue_fish_chance_bps)='integer' AND blue_fish_chance_bps BETWEEN 0 AND 10000), config_revision INTEGER
 CHECK(config_revision IS NULL OR (typeof(config_revision)='integer' AND config_revision>0)), milk_crab_chance_bps INTEGER NOT NULL DEFAULT 0 CHECK(typeof(milk_crab_chance_bps)='integer' AND milk_crab_chance_bps BETWEEN 0 AND 10000 AND blue_fish_chance_bps+milk_crab_chance_bps<=10000), UNIQUE(user_id,operation_id), CHECK((state='reserved' AND settled_at IS NULL AND revealed_at IS NULL AND ((retry_exhausted=0 AND next_attempt_at IS NOT NULL) OR (retry_exhausted=1 AND next_attempt_at IS NULL))) OR (state IN ('committed','released') AND settled_at IS NOT NULL AND next_attempt_at IS NULL AND retry_exhausted=0)), CHECK(entry_total_milli=unit_price_milli*count));`},
		{Name: "game_fishing_outcome_lengths", SQL: `CREATE TABLE game_fishing_outcome_lengths (
 batch_id TEXT NOT NULL,
 ordinal INTEGER NOT NULL CHECK(typeof(ordinal)='integer' AND ordinal BETWEEN 0 AND 9),
 length_cm TEXT NOT NULL CHECK(typeof(length_cm)='text' AND length(length_cm) BETWEEN 3 AND 128
  AND length(CAST(length_cm AS BLOB))=length(length_cm)
  AND substr(length_cm,1,1)<>'0' AND length_cm NOT GLOB '*[^0-9]*'
  AND (length(length_cm)>3 OR length_cm>='201')),
 kind TEXT NOT NULL DEFAULT 'blue' CHECK(kind IN ('blue','milk')),
 PRIMARY KEY(batch_id,ordinal),
 FOREIGN KEY(batch_id,ordinal) REFERENCES game_fishing_outcomes(batch_id,ordinal) ON DELETE CASCADE
);`},
		{Name: "game_fishing_best_lengths", SQL: `CREATE TABLE game_fishing_best_lengths (
 user_id INTEGER PRIMARY KEY REFERENCES game_fishing_best(user_id) ON DELETE CASCADE,
 length_cm TEXT NOT NULL CHECK(typeof(length_cm)='text' AND length(length_cm) BETWEEN 3 AND 128
  AND length(CAST(length_cm AS BLOB))=length(length_cm)
  AND substr(length_cm,1,1)<>'0' AND length_cm NOT GLOB '*[^0-9]*'
  AND (length(length_cm)>3 OR length_cm>='201')),
 kind TEXT NOT NULL DEFAULT 'blue' CHECK(kind IN ('blue','milk'))
);`},
		{Name: "game_fishing_length_facts", SQL: `CREATE TABLE game_fishing_length_facts (
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
   AND (length(blue_fat_fish_length_cm)>3 OR blue_fat_fish_length_cm>='201'))),
 milk_crab_length_cm TEXT CHECK(milk_crab_length_cm IS NULL OR
  (tier='legend' AND typeof(milk_crab_length_cm)='text' AND length(milk_crab_length_cm) BETWEEN 3 AND 128
   AND length(CAST(milk_crab_length_cm AS BLOB))=length(milk_crab_length_cm)
   AND substr(milk_crab_length_cm,1,1)<>'0' AND milk_crab_length_cm NOT GLOB '*[^0-9]*'
   AND (length(milk_crab_length_cm)>3 OR milk_crab_length_cm>='201'))),
 CHECK(blue_fat_fish_length_cm IS NULL OR milk_crab_length_cm IS NULL)
);`},
	}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DROP TRIGGER fishing_best_length_insert_guard;
CREATE TRIGGER fishing_best_length_insert_guard BEFORE INSERT ON game_fishing_best_lengths
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_best b JOIN game_fishing_outcome_lengths l
 ON l.batch_id=b.batch_id AND l.ordinal=b.ordinal
 WHERE b.user_id=NEW.user_id AND b.tier='legend' AND l.length_cm=NEW.length_cm AND l.kind=NEW.kind)
BEGIN SELECT RAISE(ABORT,'fishing best presentation mismatch'); END;
DROP TRIGGER fishing_best_length_update_guard;
CREATE TRIGGER fishing_best_length_update_guard BEFORE UPDATE ON game_fishing_best_lengths
WHEN NEW.user_id IS NOT OLD.user_id OR NOT EXISTS(SELECT 1 FROM game_fishing_best b
 JOIN game_fishing_outcome_lengths l ON l.batch_id=b.batch_id AND l.ordinal=b.ordinal
 WHERE b.user_id=NEW.user_id AND b.tier='legend' AND l.length_cm=NEW.length_cm AND l.kind=NEW.kind)
BEGIN SELECT RAISE(ABORT,'fishing best presentation mismatch'); END;
DROP TRIGGER fishing_presented_best_update_guard;
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
  WHERE previous.user_id=OLD.user_id AND previous.length_cm=fresh.length_cm AND previous.kind=fresh.kind)))
BEGIN SELECT RAISE(ABORT,'fishing best presentation mismatch'); END;
DROP TRIGGER fishing_length_fact_insert_guard;
CREATE TRIGGER fishing_length_fact_insert_guard BEFORE INSERT ON game_fishing_length_facts
WHEN NOT EXISTS(SELECT 1 FROM game_fishing_rank_facts f
 JOIN game_fishing_batches b ON b.id=f.batch_id_text AND b.user_id=f.user_id
 JOIN game_fishing_outcomes o ON o.batch_id=b.id AND o.ordinal=NEW.ordinal
 LEFT JOIN game_fishing_outcome_lengths l ON l.batch_id=o.batch_id AND l.ordinal=o.ordinal
 WHERE f.batch_id_text=NEW.batch_id_text AND f.aggregate_applied=1 AND b.state IN ('reserved','committed')
 AND NEW.species_key=o.species_key AND NEW.tier=o.tier AND NEW.size_cm=o.size_cm
 AND NEW.caught_at=b.created_at AND NEW.blue_fat_fish_length_cm IS CASE WHEN l.kind='blue' THEN l.length_cm END
 AND NEW.milk_crab_length_cm IS CASE WHEN l.kind='milk' THEN l.length_cm END
 AND (SELECT count(*) FROM game_fishing_outcomes all_o WHERE all_o.batch_id=b.id)=b.count
 AND NEW.ordinal=(SELECT all_o.ordinal FROM game_fishing_outcomes all_o
  LEFT JOIN game_fishing_outcome_lengths all_l ON all_l.batch_id=all_o.batch_id AND all_l.ordinal=all_o.ordinal
  WHERE all_o.batch_id=b.id ORDER BY length(COALESCE(all_l.length_cm,CAST(all_o.size_cm AS TEXT))) DESC,
  COALESCE(all_l.length_cm,CAST(all_o.size_cm AS TEXT)) COLLATE BINARY DESC,all_o.ordinal ASC LIMIT 1))
BEGIN SELECT RAISE(ABORT,'fishing length fact does not match batch maximum'); END;
DROP TRIGGER fishing_batch_probability_immutable;
CREATE TRIGGER fishing_batch_probability_immutable BEFORE UPDATE OF blue_fish_chance_bps,milk_crab_chance_bps,config_revision ON game_fishing_batches
WHEN NEW.blue_fish_chance_bps IS NOT OLD.blue_fish_chance_bps OR NEW.milk_crab_chance_bps IS NOT OLD.milk_crab_chance_bps OR NEW.config_revision IS NOT OLD.config_revision
BEGIN SELECT RAISE(ABORT,'fishing batch configuration is immutable'); END;
`); err != nil {
		return err
	}
	blue := "1000"
	err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='game_fishing_blue_fish_chance_bps'`).Scan(&blue)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	chance, err := strconv.Atoi(blue)
	if err != nil || chance < 0 || chance > 10000 {
		return errors.New("invalid fishing probability")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES('game_fishing_milk_crab_chance_bps',?,0) ON CONFLICT(key) DO NOTHING`, strconv.Itoa(min(1000, 10000-chance)))
	return err
}
