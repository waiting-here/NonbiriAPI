package observability

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func BenchmarkConnectionOccupancy(b *testing.B) {
	vault, err := secret.New(bytes.Repeat([]byte{0x43}, secret.MasterKeyBytes))
	if err != nil {
		b.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(b.TempDir(), "occupancy.sqlite")
	dbtest.EnsureOwnerOnlyParent(b, path)
	store, err := db.Open(path, vault)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, operation := range []string{"query", "transaction"} {
		for _, enabled := range []bool{false, true} {
			mode := "off"
			if enabled {
				mode = "on"
			}
			b.Run(operation+"/"+mode, func(b *testing.B) {
				store.SetConnectionObserver(nil)
				if enabled {
					store.SetConnectionObserver(NewPerformance())
				}
				b.ReportAllocs()
				for b.Loop() {
					if operation == "query" {
						var value string
						if err := store.DB().QueryRowContext(ctx, "SELECT value FROM site_config WHERE key=?", "site_name").Scan(&value); err != nil {
							b.Fatal(err)
						}
					} else {
						tx, err := store.DB().BeginTx(ctx, nil)
						if err != nil {
							b.Fatal(err)
						}
						for range 8 {
							var value string
							if err := tx.QueryRowContext(ctx, "SELECT value FROM site_config WHERE key=?", "site_name").Scan(&value); err != nil {
								b.Fatal(err)
							}
						}
						if _, err := tx.ExecContext(ctx, "UPDATE site_config SET updated_at=updated_at+1 WHERE key=?", "site_name"); err != nil {
							b.Fatal(err)
						}
						if err := tx.Commit(); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}
