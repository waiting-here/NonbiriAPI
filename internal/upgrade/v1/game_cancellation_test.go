package v1

import (
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"strings"
	"testing"
)

func TestGameCancellationUpgradePreservesChildrenAndConstraints(t *testing.T) {
	store, err := db.Open(bootstrapTestPath(t, "games.sqlite"), bootstrapTestVault(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database := store.DB()
	ctx := t.Context()
	fresh, err := db.SchemaManifestHash(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	dbtest.RestoreSchema58GameCancellation(t, database)
	id := "rps_" + strings.Repeat("A", 22)
	mustExec(t, database, `INSERT INTO game_rps_summaries VALUES(?,'quick',1,1000,0,0,0,1,2,'quick_resolved',
 zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),2592002)`, id)
	mustExec(t, database, `INSERT INTO game_rps_summary_seats VALUES(?,0,NULL,zeroblob(32),zeroblob(32),0,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),NULL,NULL)`, id)
	mustExec(t, database, `INSERT INTO game_rps_summary_presentation VALUES(?,0,zeroblob(16),zeroblob(16))`, id)
	mustExec(t, database, `INSERT INTO game_random_proofs(resource_id,game_key,rps_summary_id,private_json) VALUES(?,'rps',?,json_object('game','rps','resource_id',?,'algorithm','hmac-sha256-reject64-v1','seed',?,'commitment',?))`, id, id, id, strings.Repeat("0", 64), strings.Repeat("a", 64))
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := UpgradeGameCancellation(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if actual, err := db.SchemaManifestHash(ctx, database); err != nil || actual != fresh {
		t.Fatalf("upgraded schema %s want %s: %v", actual, fresh, err)
	}
	var found int
	if err := database.QueryRow(`SELECT count(*) FROM game_rps_summaries s JOIN game_rps_summary_seats a USING(session_id)
 JOIN game_rps_summary_presentation p USING(session_id,seat_no) JOIN game_random_proofs r ON r.rps_summary_id=s.session_id
 WHERE s.session_id=? AND s.terminal_reason='quick_resolved' AND r.resource_id=?`, id, id).Scan(&found); err != nil || found != 1 {
		t.Fatal(found, err)
	}
	if err := database.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&found); err != nil || found != 0 {
		t.Fatal(found, err)
	}
}
