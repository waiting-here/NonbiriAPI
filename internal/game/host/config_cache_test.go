package host

import (
	"context"
	"testing"
)

func TestConfigurationCacheFollowsTransactionValuesAndRollback(t *testing.T) {
	f := newGameFixture(t, nil)
	ctx := context.Background()
	read := func(want bool) {
		t.Helper()
		config, err := f.service.ReadGamesConfig(ctx)
		if err != nil || config.MasterEnabled != want {
			t.Fatalf("enabled=%v err=%v", config.MasterEnabled, err)
		}
	}
	read(true)
	tx, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE site_config SET value='0' WHERE key IN ('games_enabled','game_fishing_enabled')`); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := f.service.readSnapshot(ctx, tx)
	if err != nil || snapshot.Enabled() {
		t.Fatalf("transaction enabled=%v err=%v", snapshot.Enabled(), err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	read(true)
	if _, err := f.database.Exec(`UPDATE site_config SET value='0' WHERE key IN ('games_enabled','game_fishing_enabled')`); err != nil {
		t.Fatal(err)
	}
	read(false)
}
