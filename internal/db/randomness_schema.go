package db

import (
	"context"
	"database/sql"
	"errors"
)

const preRandomnessManifestHash = "639040c75ac740c81988c3f10da423b8f5161ab1dcf8e53cf9641eada28c755a"

const gameRandomnessSchema = `
CREATE TABLE game_random_proofs (
 resource_id TEXT PRIMARY KEY NOT NULL,
 game_key TEXT NOT NULL CHECK(game_key IN ('fishing','linklink','rps','bidding','likes','blackjack')),
 fishing_id TEXT UNIQUE REFERENCES game_fishing_batches(id) ON DELETE CASCADE,
 linklink_id TEXT UNIQUE REFERENCES game_linklink_sessions(id) ON DELETE CASCADE,
 linklink_summary_id TEXT UNIQUE REFERENCES game_linklink_summaries(session_id) ON DELETE CASCADE,
 rps_id TEXT UNIQUE REFERENCES game_rps_sessions(id) ON DELETE CASCADE,
 rps_summary_id TEXT UNIQUE REFERENCES game_rps_summaries(session_id) ON DELETE CASCADE,
 duel_id TEXT UNIQUE REFERENCES game_duel_sessions(id) ON DELETE CASCADE,
 blackjack_id TEXT UNIQUE REFERENCES game_blackjack_sessions(id) ON DELETE CASCADE,
 private_json TEXT NOT NULL CHECK(typeof(private_json)='text' AND length(CAST(private_json AS BLOB)) BETWEEN 1 AND 2097152 AND json_valid(private_json)),
 CHECK(
  (game_key='fishing' AND fishing_id IS resource_id AND linklink_id IS NULL AND linklink_summary_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND duel_id IS NULL AND blackjack_id IS NULL) OR
  (game_key='linklink' AND ((linklink_id IS resource_id AND linklink_summary_id IS NULL) OR (linklink_summary_id IS resource_id AND linklink_id IS NULL)) AND fishing_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND duel_id IS NULL AND blackjack_id IS NULL) OR
  (game_key='rps' AND ((rps_id IS resource_id AND rps_summary_id IS NULL) OR (rps_summary_id IS resource_id AND rps_id IS NULL)) AND fishing_id IS NULL AND linklink_id IS NULL AND linklink_summary_id IS NULL AND duel_id IS NULL AND blackjack_id IS NULL) OR
  (game_key IN ('bidding','likes') AND duel_id IS resource_id AND fishing_id IS NULL AND linklink_id IS NULL AND linklink_summary_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND blackjack_id IS NULL) OR
  (game_key='blackjack' AND blackjack_id IS resource_id AND fishing_id IS NULL AND linklink_id IS NULL AND linklink_summary_id IS NULL AND rps_id IS NULL AND rps_summary_id IS NULL AND duel_id IS NULL)
 ),
 CHECK(json_extract(private_json,'$.resource_id') IS resource_id AND json_extract(private_json,'$.game') IS game_key AND json_extract(private_json,'$.algorithm') IS 'hmac-sha256-reject64-v1'),
 CHECK(json_type(private_json,'$.seed') IS 'text' AND length(json_extract(private_json,'$.seed'))=64 AND json_extract(private_json,'$.seed') NOT GLOB '*[^0-9a-f]*'),
 CHECK(json_type(private_json,'$.commitment') IS 'text' AND length(json_extract(private_json,'$.commitment'))=64 AND json_extract(private_json,'$.commitment') NOT GLOB '*[^0-9a-f]*')
) STRICT;
CREATE TRIGGER game_random_proof_identity_guard BEFORE UPDATE ON game_random_proofs
WHEN NEW.resource_id IS NOT OLD.resource_id OR NEW.game_key IS NOT OLD.game_key
 OR NEW.fishing_id IS NOT OLD.fishing_id OR NEW.duel_id IS NOT OLD.duel_id OR NEW.blackjack_id IS NOT OLD.blackjack_id
 OR ((NEW.linklink_id IS NOT OLD.linklink_id OR NEW.linklink_summary_id IS NOT OLD.linklink_summary_id) AND NOT (OLD.linklink_id IS OLD.resource_id AND OLD.linklink_summary_id IS NULL AND NEW.linklink_id IS NULL AND NEW.linklink_summary_id IS OLD.resource_id))
 OR ((NEW.rps_id IS NOT OLD.rps_id OR NEW.rps_summary_id IS NOT OLD.rps_summary_id) AND NOT (OLD.rps_id IS OLD.resource_id AND OLD.rps_summary_id IS NULL AND NEW.rps_id IS NULL AND NEW.rps_summary_id IS OLD.resource_id))
 OR json_extract(NEW.private_json,'$.seed') IS NOT json_extract(OLD.private_json,'$.seed')
 OR json_extract(NEW.private_json,'$.commitment') IS NOT json_extract(OLD.private_json,'$.commitment')
 OR json_extract(NEW.private_json,'$.rules') IS NOT json_extract(OLD.private_json,'$.rules')
BEGIN SELECT RAISE(ABORT,'game random commitment is immutable'); END;
`

func applyRandomnessExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preRandomnessManifestHash {
		return errors.New("unrecognized game randomness source manifest")
	}
	_, err = tx.ExecContext(ctx, gameRandomnessSchema)
	return err
}
