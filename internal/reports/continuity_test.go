package reports

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/continuity"
)

func TestReportAccountWindowMergesLegacyOnceAndSurvivesRegistration(t *testing.T) {
	e := newReportTestEnvironment(t)
	ctx := context.Background()
	actor := e.seedActor(t, false, 1)
	var discord string
	if err := e.store.DB().QueryRow(`SELECT discord_id FROM users WHERE id=?`, actor.UserID).Scan(&discord); err != nil {
		t.Fatal(err)
	}
	now := e.clock.Load()
	start := now - now%600
	legacy, _ := e.repository.keys.rateDigest("account", []byte(strconv.FormatInt(actor.UserID, 10)))
	if _, err := e.store.DB().Exec(`INSERT INTO report_rate_buckets VALUES('account',?,?,6,?,?)`, legacy[:], start, now, start+1200); err != nil {
		t.Fatal(err)
	}
	accept := func(actor authz.Actor, sequence byte, at int64) error {
		tx, err := e.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		err = e.repository.acceptRatesTx(ctx, tx, &actor, [16]byte{sequence}, [32]byte{sequence}, at)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	for i := byte(1); i <= 4; i++ {
		if err := accept(actor, i, now); err != nil {
			t.Fatal(err)
		}
	}
	if e.rowCount(t, `SELECT count(*) FROM report_rate_buckets WHERE scope='account' AND scope_hash=?`, legacy[:]) != 0 {
		t.Fatal("legacy rows survived merge")
	}
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.repository.DetachUserForDeletion(ctx, tx, actor.UserID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, actor.UserID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	actor = e.seedActor(t, false, 1)
	identities, err := continuity.New(e.store.DB(), e.vault)
	if err != nil {
		t.Fatal(err)
	}
	defer identities.Close()
	tx, err = e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM user_continuity_identities WHERE user_id=?`, actor.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE users SET discord_id=? WHERE id=?`, discord, actor.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := identities.BindUserTx(ctx, tx, actor.UserID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := accept(actor, 5, now); !errors.Is(err, ErrRateLimited) {
		t.Fatal("registration reset report rate", err)
	}
	if err := accept(actor, 6, start+600); err != nil {
		t.Fatal("original bucket boundary extended", err)
	}
}
