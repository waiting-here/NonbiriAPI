package db

const gameplayGovernanceSchema = `
ALTER TABLE game_fishing_batches ADD COLUMN blue_fish_chance_bps INTEGER NOT NULL DEFAULT 1000
 CHECK(typeof(blue_fish_chance_bps)='integer' AND blue_fish_chance_bps BETWEEN 0 AND 10000);
ALTER TABLE game_fishing_batches ADD COLUMN config_revision INTEGER
 CHECK(config_revision IS NULL OR (typeof(config_revision)='integer' AND config_revision>0));
CREATE TRIGGER fishing_batch_probability_immutable BEFORE UPDATE OF blue_fish_chance_bps,config_revision ON game_fishing_batches
WHEN NEW.blue_fish_chance_bps IS NOT OLD.blue_fish_chance_bps OR NEW.config_revision IS NOT OLD.config_revision
BEGIN SELECT RAISE(ABORT,'fishing batch configuration is immutable'); END;

ALTER TABLE game_rank_expiry_work ADD COLUMN net_game_delta_sign INTEGER NOT NULL DEFAULT 0 CHECK(net_game_delta_sign IN(-1,0,1));
ALTER TABLE game_rank_expiry_work ADD COLUMN net_game_delta_mag BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000'
 CHECK(length(net_game_delta_mag)=32 AND (net_game_delta_sign=0)=(net_game_delta_mag=zeroblob(32)));
ALTER TABLE game_rank_expiry_work ADD COLUMN net_fishing_delta_sign INTEGER NOT NULL DEFAULT 0 CHECK(net_fishing_delta_sign IN(-1,0,1));
ALTER TABLE game_rank_expiry_work ADD COLUMN net_fishing_delta_mag BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000'
 CHECK(length(net_fishing_delta_mag)=32 AND (net_fishing_delta_sign=0)=(net_fishing_delta_mag=zeroblob(32)));
ALTER TABLE game_rank_expiry_work ADD COLUMN net_blackjack_delta_sign INTEGER NOT NULL DEFAULT 0 CHECK(net_blackjack_delta_sign IN(-1,0,1));
ALTER TABLE game_rank_expiry_work ADD COLUMN net_blackjack_delta_mag BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000'
 CHECK(length(net_blackjack_delta_mag)=32 AND (net_blackjack_delta_sign=0)=(net_blackjack_delta_mag=zeroblob(32)));

ALTER TABLE game_rank_expiry_work ADD COLUMN net_fishing_last_seq BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(length(net_fishing_last_seq)=16);
ALTER TABLE game_rank_expiry_work ADD COLUMN net_blackjack_last_seq BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(length(net_blackjack_last_seq)=16);

CREATE TABLE game_rank_net_rebuild (
 id INTEGER PRIMARY KEY CHECK(id=1),
 phase INTEGER NOT NULL CHECK(phase IN (0,1,2)),
 through_seq BLOB CHECK(through_seq IS NULL OR (length(through_seq)=16 AND through_seq>zeroblob(16))),
 last_seq BLOB NOT NULL CHECK(length(last_seq)=16),
 CHECK(phase=0 OR through_seq IS NOT NULL)
) STRICT;
CREATE TABLE game_rank_net_rebuild_totals (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 board TEXT NOT NULL CHECK(board IN ('game_net_profit','fishing_net_profit','blackjack_net_profit')),
 amount_sign INTEGER NOT NULL CHECK(amount_sign IN (-1,0,1)),
 amount_mag BLOB NOT NULL CHECK(length(amount_mag)=32),
 achieved_at INTEGER NOT NULL CHECK(achieved_at BETWEEN 0 AND 253402300799),
 achieved_seq BLOB NOT NULL CHECK(length(achieved_seq)=16),
 PRIMARY KEY(user_id,board),
 CHECK((amount_sign=0)=(amount_mag=zeroblob(32)))
) STRICT;
`
