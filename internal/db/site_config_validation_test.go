package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSiteConfigReuseReadsCommittedContents(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "config-reads.sqlite"))
	before, err := store.GetAllSiteConfigValuesContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	original := before["site_name"]
	before["site_name"] = "caller mutation"
	delete(before, "games_enabled")
	if got, err := store.GetSiteConfigValueContext(t.Context(), "site_name"); err != nil || got != original {
		t.Fatalf("caller corrupted validation snapshot: %q %v", got, err)
	}
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE site_config SET value='uncommitted' WHERE key='site_name'`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if got, err := store.GetSiteConfigValueContext(ctx, "site_name"); err != nil || got != original {
		t.Fatalf("reader blocked on writer or saw uncommitted value: %q %v", got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// A change without a revision bump must still be observed.
	if _, err := store.DB().Exec(`UPDATE site_config SET value='committed' WHERE key='site_name'`); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetSiteConfigValueContext(t.Context(), "site_name"); err != nil || got != "committed" {
		t.Fatalf("committed value hidden by reuse: %q %v", got, err)
	}
	if err := store.SetSiteConfigValue("site_name", "validated setter"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetSiteConfigValueContext(t.Context(), "site_name"); err != nil || got != "validated setter" {
		t.Fatalf("setter not visible: %q %v", got, err)
	}

	if err := store.SetSiteConfigValue("model_request_body_limit_mib", "10"); err != nil {
		t.Fatal(err)
	}
	values, err := store.GetAllSiteConfigValues()
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := strconv.ParseInt(values["checkin_award_max_milli"], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ name, query, restore string }{
		{"unknown", `INSERT INTO site_config VALUES('unknown_config','x',0)`, `DELETE FROM site_config WHERE key='unknown_config'`},
		{"invalid", `UPDATE site_config SET value='65' WHERE key='model_request_body_limit_mib'`, `UPDATE site_config SET value='` + values["model_request_body_limit_mib"] + `' WHERE key='model_request_body_limit_mib'`},
		{"combination", fmt.Sprintf(`UPDATE site_config SET value='%d' WHERE key='checkin_award_min_milli'`, maximum+1), `UPDATE site_config SET value='` + values["checkin_award_min_milli"] + `' WHERE key='checkin_award_min_milli'`},
		{"missing", `DELETE FROM site_config WHERE key='maintenance_mode'`, `INSERT INTO site_config VALUES('maintenance_mode','` + values["maintenance_mode"] + `',0)`},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := store.DB().Exec(change.query); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetSiteConfigValueContext(t.Context(), "site_name"); err == nil {
				t.Fatal("invalid current snapshot bypassed full validation")
			}
			if _, err := store.DB().Exec(change.restore); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetAllSiteConfigValues(); err != nil {
				t.Fatalf("repaired snapshot retained failure: %v", err)
			}
		})
	}
}

func TestSiteConfigReuseConcurrentSnapshotsAndReaderLifecycle(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "config-concurrent.sqlite"))
	if _, err := store.GetAllSiteConfigValues(); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 9)
	for range 8 {
		go func() {
			for range 12 {
				values, err := store.GetAllSiteConfigValuesContext(t.Context())
				if err != nil {
					errs <- err
					return
				}
				values["site_name"] = "mutated locally"
			}
			errs <- nil
		}()
	}
	go func() {
		for i := range 12 {
			if err := store.SetSiteConfigValue("site_name", fmt.Sprintf("snapshot %d", i)); err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()
	for range 9 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got, err := store.GetSiteConfigValueContext(t.Context(), "site_name"); err != nil || got != "snapshot 11" {
		t.Fatalf("last commit hidden: %q %v", got, err)
	}
	var held []*sql.Conn
	defer func() {
		for _, conn := range held {
			conn.Close()
		}
	}()
	for range readPoolConnections {
		conn, err := store.ReadDB().Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := store.GetAllSiteConfigValuesContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reader wait ignored cancellation or used old snapshot: %v", err)
	}
	for _, conn := range held {
		conn.Close()
	}
	held = nil
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAllSiteConfigValues(); err == nil {
		t.Fatal("closed reader returned a cached result")
	}
}

func TestSiteConfigReuseRejectsEmptyInitialSnapshot(t *testing.T) {
	database := openDMLTestDB(t)
	if _, err := database.Exec(`CREATE TABLE site_config(key TEXT PRIMARY KEY,value TEXT)`); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: database, readDB: database}
	if _, err := store.GetAllSiteConfigValues(); err == nil {
		t.Fatal("empty snapshot treated as previously validated")
	}
}
