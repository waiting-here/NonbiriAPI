CREATE TEMP TABLE saved_heart_appearance AS SELECT user_id,selected,updated_at FROM user_appearance;
DROP TABLE user_appearance;
CREATE TABLE user_appearance (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 selected TEXT NOT NULL CHECK(length(selected) BETWEEN 1 AND 64),
 updated_at INTEGER NOT NULL CHECK(updated_at BETWEEN 0 AND 253402300799)
) STRICT;
INSERT INTO user_appearance SELECT * FROM saved_heart_appearance;
DROP TABLE saved_heart_appearance;
UPDATE schema_state SET version=15 WHERE id=1;
