package db

import (
	"context"
	"testing"
)

func TestCustomPresetUpgradeBoundsOwnershipAndDeletion(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	user := hostileInsertUser(t, database, "preset owner", 0, 1)
	other := hostileInsertUser(t, database, "another preset owner", 0, 1)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	const valid = `{"role":"ChatGPT","harness":null,"skills":["PUB01"]}`
	const insert = `INSERT INTO game_likes_loadouts(user_id,slot,revision,mode,loadout_json,updated_at) VALUES(?,?,1,'quick',?,1)`
	for slot := 1; slot <= 10; slot++ {
		hostileMustExec(t, database, insert, user, slot, valid)
	}
	hostileMustExec(t, database, insert, other, 1, valid)
	for _, slot := range []any{0, 11, 1.5, nil} {
		hostileMustFail(t, database, insert, other, slot, valid)
	}
	for _, body := range []string{`{}`, `null`, `{"role":"ChatGPT","harness":null,"skills":[]}`, `{"role":"ChatGPT","skills":["PUB01"]}`} {
		hostileMustFail(t, database, insert, other, 2, body)
	}
	hostileMustFail(t, database, `UPDATE game_likes_loadouts SET user_id=?,revision=2 WHERE user_id=?`, other, user)
	hostileMustFail(t, database, `UPDATE game_likes_loadouts SET slot=2,revision=2 WHERE user_id=? AND slot=1`, other)
	hostileMustFail(t, database, `UPDATE game_likes_loadouts SET revision=3 WHERE user_id=? AND slot=1`, user)
	hostileMustExec(t, database, `UPDATE game_likes_loadouts SET revision=2,mode='standard',updated_at=2 WHERE user_id=? AND slot=1`, user)
	hostileMustExec(t, database, `INSERT INTO user_deletion_markers(user_id) VALUES(?)`, other)
	hostileMustFail(t, database, insert, other, 2, valid)
	hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, user)
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM game_likes_loadouts WHERE user_id=?`, user).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM game_likes_loadouts WHERE user_id=?`, other).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
}
