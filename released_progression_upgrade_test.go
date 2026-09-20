package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestReleasedProgressionUpgrade(t *testing.T) {
	for _, name := range []string{"DUEL", "BLACKJACK"} {
		t.Run(name, func(t *testing.T) {
			source := os.Getenv("NONBIRI_DUAL_" + name + "_FIXTURE")
			if source == "" {
				t.Skip("released-source gate supplies this fixture")
			}
			body, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "upgrade.db")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			prior, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			before := releasedImages(t, prior)
			var now int64
			query := `SELECT max(started_at) FROM game_duel_sessions`
			if name == "BLACKJACK" {
				query = `SELECT observed_at FROM game_blackjack_clock WHERE id=1`
			}
			if err := prior.QueryRow(query).Scan(&now); err != nil {
				t.Fatal(err)
			}
			if name == "DUEL" {
				upgradeScalar(t, prior, `SELECT count(*) FROM game_duel_sessions WHERE state='active'`, 2)
				upgradeScalar(t, prior, `SELECT count(*) FROM game_duel_sessions WHERE state='terminal'`, 1)
			} else {
				upgradeScalar(t, prior, `SELECT count(*) FROM game_blackjack_entries WHERE state='seated'`, 9)
				upgradeScalar(t, prior, `SELECT count(*) FROM game_blackjack_entries WHERE state='waiting'`, 1)
			}
			if err := prior.Close(); err != nil {
				t.Fatal(err)
			}
			vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
			if err != nil {
				t.Fatal(err)
			}
			defer vault.Close()
			store, err := db.Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			requireReleasedRows(t, store.DB(), before)
			var epoch int64
			if err := store.DB().QueryRow(`SELECT started_at FROM game_statistics_epoch WHERE id=1`).Scan(&epoch); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				app, err := buildApplicationWithGameClock(auditConfig(), store, vault, func() time.Time { return time.Unix(now, 0) })
				if err != nil {
					t.Fatal("released game recovery", err)
				}
				if err := app.games.ValidatePersistedState(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := app.Close(); err != nil {
					t.Fatal(err)
				}
				if name == "DUEL" {
					upgradeScalar(t, store.DB(), `SELECT count(*) FROM game_duel_sessions WHERE state='active'`, 0)
					upgradeScalar(t, store.DB(), `SELECT count(*) FROM game_duel_sessions WHERE reason='server_restart'`, 2)
					upgradeScalar(t, store.DB(), `SELECT count(*) FROM game_duel_sessions WHERE reason='surrender'`, 1)
				} else {
					upgradeScalar(t, store.DB(), `SELECT count(*) FROM game_blackjack_entries WHERE state='released'`, 9)
					upgradeScalar(t, store.DB(), `SELECT count(*) FROM game_blackjack_entries WHERE state='waiting'`, 1)
					// An undealt table has no result to retain; its nine entries
					// are refunded and detached while the waiter keeps its place.
					upgradeScalar(t, store.DB(), `SELECT count(*) FROM game_blackjack_sessions`, 0)
				}
				upgradeScalar(t, store.DB(), `SELECT count(*) FROM game_onboarding_completions`, 0)
				upgradeScalar(t, store.DB(), `SELECT count(*) FROM game_rank_events`, 0)
				upgradeScalar(t, store.DB(), `SELECT count(*) FROM abuse_cases`, 0)
				upgradeScalar(t, store.DB(), `SELECT started_at FROM game_statistics_epoch WHERE id=1`, epoch)
				checkUpgradedLedger(t, store)
			}
		})
	}
}
