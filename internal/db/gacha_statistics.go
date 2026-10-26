package db

// Aggregate rows deliberately have no user or live-pool foreign key. Membership
// is needed only while an account can draw again; deleting it never subtracts
// from the separately stored lifetime counters.
const GachaStatisticsSQL = `CREATE TABLE gacha_pool_statistics (
 pool_id TEXT PRIMARY KEY CHECK(pool_id='permanent' OR (length(pool_id)=25 AND substr(pool_id,1,3)='gp_')),
 config TEXT NOT NULL CHECK(json_valid(config) AND json_type(config)='object' AND length(config)<=32768),
 roster TEXT NOT NULL CHECK(json_valid(roster) AND json_type(roster)='array'),
 total_draws BLOB NOT NULL CHECK(length(total_draws)=16),
 participant_count BLOB NOT NULL CHECK(length(participant_count)=16)
) STRICT;
CREATE TABLE gacha_card_statistics (
 pool_id TEXT NOT NULL REFERENCES gacha_pool_statistics(pool_id),
 card_id TEXT NOT NULL CHECK(length(card_id) BETWEEN 1 AND 80),
 draw_count BLOB NOT NULL CHECK(length(draw_count)=16),
 participant_count BLOB NOT NULL CHECK(length(participant_count)=16),
 first_acquisition_count BLOB NOT NULL CHECK(length(first_acquisition_count)=16),
 PRIMARY KEY(pool_id,card_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE gacha_pool_participants (
 pool_id TEXT NOT NULL REFERENCES gacha_pool_statistics(pool_id),
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 PRIMARY KEY(pool_id,user_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_gacha_pool_participants_user ON gacha_pool_participants(user_id,pool_id);
CREATE TABLE gacha_card_participants (
 pool_id TEXT NOT NULL,
 card_id TEXT NOT NULL,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 PRIMARY KEY(pool_id,card_id,user_id),
 FOREIGN KEY(pool_id,card_id) REFERENCES gacha_card_statistics(pool_id,card_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX idx_gacha_card_participants_user ON gacha_card_participants(user_id,pool_id,card_id);
`
