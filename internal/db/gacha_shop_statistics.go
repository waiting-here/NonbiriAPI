package db

const GachaShopStatisticsSQL = `CREATE TABLE gacha_shop_statistics (
 id INTEGER PRIMARY KEY CHECK(id=1),
 total_redemptions BLOB NOT NULL CHECK(length(total_redemptions)=16),
 participant_count BLOB NOT NULL CHECK(length(participant_count)=16)
) STRICT;
INSERT INTO gacha_shop_statistics VALUES(1,zeroblob(16),zeroblob(16));
CREATE TABLE gacha_shop_card_statistics (
 card_id TEXT PRIMARY KEY CHECK(length(card_id) BETWEEN 1 AND 80),
 redemption_count BLOB NOT NULL CHECK(length(redemption_count)=16)
) STRICT;
CREATE TABLE gacha_shop_participants (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE
) STRICT;
`
