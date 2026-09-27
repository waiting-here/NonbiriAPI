package db

const likesLoadoutSchema = `
CREATE TABLE game_likes_loadouts (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 slot INTEGER NOT NULL CHECK(typeof(slot)='integer' AND slot BETWEEN 1 AND 10),
 revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision BETWEEN 1 AND 9223372036854775807),
 mode TEXT NOT NULL CHECK(mode IN ('quick','standard')),
 loadout_json TEXT NOT NULL CHECK(typeof(loadout_json)='text' AND length(CAST(loadout_json AS BLOB)) BETWEEN 1 AND 4096
  AND json_valid(loadout_json) AND COALESCE(json_type(loadout_json),'')='object'
  AND COALESCE(json_type(loadout_json,'$.role'),'')='text'
  AND COALESCE(json_type(loadout_json,'$.harness'),'') IN ('null','text')
  AND COALESCE(json_type(loadout_json,'$.skills'),'')='array'
  AND json_array_length(loadout_json,'$.skills') BETWEEN 1 AND 6),
 updated_at INTEGER NOT NULL CHECK(typeof(updated_at)='integer' AND updated_at BETWEEN 0 AND 253402300799),
 PRIMARY KEY(user_id,slot)
);
CREATE TRIGGER game_likes_loadouts_insert_guard BEFORE INSERT ON game_likes_loadouts
WHEN NEW.revision<>1 OR NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.user_id AND is_admin=0 AND is_banned=0)
 OR EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=NEW.user_id)
BEGIN SELECT RAISE(ABORT,'invalid custom preset owner or revision'); END;
CREATE TRIGGER game_likes_loadouts_update_guard BEFORE UPDATE ON game_likes_loadouts
WHEN NEW.user_id<>OLD.user_id OR NEW.slot<>OLD.slot OR OLD.revision=9223372036854775807
 OR NEW.revision<>OLD.revision+1 OR NEW.updated_at<OLD.updated_at
 OR NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.user_id AND is_admin=0 AND is_banned=0)
 OR EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=NEW.user_id)
BEGIN SELECT RAISE(ABORT,'invalid custom preset update'); END;
`
