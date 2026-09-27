package flowcontrol

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestRPMDeletionRecoveryKeepsOriginalWindowAndResetsOnlyLimits(t *testing.T) {
	ctx := context.Background()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "window.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identities, err := continuity.New(store.DB(), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer identities.Close()
	insert := func() int64 {
		zero := db.EncodeU128(db.U128{})
		r, err := store.DB().Exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('555','fixture',?,?,?,?,?,?,?,?,1,1)`, zero, zero, zero, zero, zero, zero, zero, zero)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := r.LastInsertId()
		return id
	}
	user := insert()
	clock := newFakeClock()
	config := testRPMConfig()
	config.MaxKeyBytes = 64
	newController := func() *Controller {
		c, err := newWithClock(Config{RPM: config, Continuity: identities}, clock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	controller := newController()
	for range 2 {
		r, _, err := controller.Admit(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		r.Commit()
	}
	begin := func() *sql.Tx {
		tx, err := store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	tx := begin()
	if err := controller.PreserveWindowTx(ctx, tx, user, clock.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	var count int
	store.DB().QueryRow(`SELECT count(*) FROM identity_window_events`).Scan(&count)
	if count != 0 {
		t.Fatal("failed deletion left persistence")
	}
	tx = begin()
	if err := controller.PreserveWindowTx(ctx, tx, user, clock.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	newUser := insert()
	if newUser == user {
		t.Fatal("old account reused")
	}
	for _, c := range []*Controller{controller, newController()} {
		for range 2 {
			if _, _, err := c.Admit(ctx, newUser); !errors.Is(err, ErrRateLimited) {
				t.Fatal("same identity reset live window", err)
			}
		}
	}
	clock.Advance(10 * time.Second)
	r, _, err := controller.Admit(ctx, newUser)
	if err != nil {
		t.Fatal("old boundary extended", err)
	}
	r.Release()
	if _, _, err := controller.Admit(ctx, user); !errors.Is(err, ErrInvalidUser) {
		t.Fatal("old account attached to new identity", err)
	}
}
