package fishing_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	_ "modernc.org/sqlite"
)

func TestHomeSummaryTxClosedSourcesOwnershipAndOrder(t *testing.T) {
	database := openFishingHomeDatabase(t)
	seedFishingHomeRow(t, database, "fb_AAAAAAAAAAAAAAAAAAAAAA", 1, "reserved", 0, int64Pointer(50), 10, nil)
	seedFishingHomeRow(t, database, "fb_AAAAAAAAAAAAAAAAAAAAAQ", 1, "committed", 0, nil, 30, nil)
	seedFishingHomeRow(t, database, "fb_AAAAAAAAAAAAAAAAAAAAAg", 1, "committed", 0, nil, 20, nil)
	revealed := int64(40)
	seedFishingHomeRow(t, database, "fb_AAAAAAAAAAAAAAAAAAAAAw", 1, "committed", 0, nil, 15, &revealed)
	seedFishingHomeRow(t, database, "fb_QAAAAAAAAAAAAAAAAAAAAA", 1, "released", 0, nil, 5, nil)
	seedFishingHomeRow(t, database, "fb_gAAAAAAAAAAAAAAAAAAAAA", 2, "committed", 0, nil, 1, nil)

	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := fishing.HomeSummaryTx(context.Background(), tx, 1)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if len(summary.Continue) != 1 || summary.Continue[0].ResourceID != "fb_AAAAAAAAAAAAAAAAAAAAAA" ||
		summary.Continue[0].State != fishing.HomeStateSettlementPending {
		_ = tx.Rollback()
		t.Fatalf("continue=%+v", summary.Continue)
	}
	if len(summary.PendingResults) != 2 || summary.PendingResults[0].CreatedAt != 20 ||
		summary.PendingResults[1].CreatedAt != 30 {
		_ = tx.Rollback()
		t.Fatalf("pending=%+v", summary.PendingResults)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var unrevealed int
	if err := database.QueryRow(`SELECT COUNT(*) FROM game_fishing_batches WHERE user_id=1 AND state='committed' AND revealed_at IS NULL`).Scan(&unrevealed); err != nil || unrevealed != 2 {
		t.Fatalf("read changed ACK state: count=%d err=%v", unrevealed, err)
	}

	foreignTx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := fishing.HomeSummaryTx(context.Background(), foreignTx, 2)
	_ = foreignTx.Rollback()
	if err != nil || len(foreign.Continue) != 0 || len(foreign.PendingResults) != 1 || foreign.PendingResults[0].CreatedAt != 1 {
		t.Fatalf("foreign summary=(%+v,%v)", foreign, err)
	}
}

func TestHomeSummaryTxRecoveryEmptyAndInvariant(t *testing.T) {
	database := openFishingHomeDatabase(t)
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := fishing.HomeSummaryTx(context.Background(), tx, 1)
	_ = tx.Rollback()
	if err != nil || empty.Continue == nil || empty.PendingResults == nil || len(empty.Continue) != 0 || len(empty.PendingResults) != 0 {
		t.Fatalf("empty=(%#v,%v)", empty, err)
	}

	seedFishingHomeRow(t, database, "fb_AAAAAAAAAAAAAAAAAAAAAA", 1, "reserved", 1, nil, 10, nil)
	tx, err = database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := fishing.HomeSummaryTx(context.Background(), tx, 1)
	_ = tx.Rollback()
	if err != nil || len(recovery.Continue) != 1 || recovery.Continue[0].State != fishing.HomeStateRecoveryRequired {
		t.Fatalf("recovery=(%+v,%v)", recovery, err)
	}

	if _, err := database.Exec(`UPDATE game_fishing_batches SET retry_exhausted=2 WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	tx, err = database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fishing.HomeSummaryTx(context.Background(), tx, 1)
	_ = tx.Rollback()
	if !errors.Is(err, fishing.ErrHomeInvariant) {
		t.Fatalf("invalid persisted state error=%v", err)
	}
}

func TestHomeSummaryTxBoundsPendingProjection(t *testing.T) {
	database := openFishingHomeDatabase(t)
	for index := 0; index <= 100; index++ {
		seedFishingHomeRow(t, database, fmt.Sprintf("fb_pending_%03d", index), 1, "committed", 0, nil, int64(index), nil)
	}
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fishing.HomeSummaryTx(context.Background(), tx, 1)
	_ = tx.Rollback()
	if !errors.Is(err, fishing.ErrHomeResourceLimit) {
		t.Fatalf("pending projection error=%v", err)
	}
}

func openFishingHomeDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fishing-home.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`CREATE TABLE game_fishing_batches(
id TEXT PRIMARY KEY,user_id INTEGER NOT NULL,state TEXT NOT NULL,retry_exhausted INTEGER NOT NULL,
next_attempt_at INTEGER,created_at INTEGER NOT NULL,revealed_at INTEGER)`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func seedFishingHomeRow(t *testing.T, database *sql.DB, id string, userID int64, state string, retry int, next *int64, created int64, revealed *int64) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO game_fishing_batches(id,user_id,state,retry_exhausted,next_attempt_at,created_at,revealed_at) VALUES(?,?,?,?,?,?,?)`,
		id, userID, state, retry, next, created, revealed); err != nil {
		t.Fatal(err)
	}
}

func int64Pointer(value int64) *int64 { return &value }
