package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
)

func TestOrphanSecretEmptyMaintenanceUsesReader(t *testing.T) {
	f := newClaimFixture(t)
	user := f.seedUser("live-secret", false)
	f.seedKey(user, "live-secret")
	const now = int64(5_000)
	future := now - int64(OrphanSecretTTL.Seconds()) + 1
	seedUnreferencedRecoverySecret(t, f, 121, &future)
	var count int
	if err := f.store.ReadDB().QueryRow(`SELECT count(*) FROM endpoint_key_secrets`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	dbtest.WithoutWriter(t, f.db, f.store.ReadDB(), func(ctx context.Context) {
		report, err := f.service.MaintainOrphanSecretsAt(ctx, now, 10, time.Time{})
		if err != nil || report != (MaintenanceReport{}) {
			t.Fatalf("empty maintenance = %+v, %v", report, err)
		}
	})
	// New work after the empty snapshot is picked up by the next pass.
	id := seedUnreferencedRecoverySecret(t, f, 122, nil)
	report, err := f.service.MaintainOrphanSecretsAt(t.Context(), now, 10, time.Time{})
	if err != nil || report.Marked != 1 || report.Deleted != 0 || report.More {
		t.Fatalf("new orphan maintenance = %+v, %v", report, err)
	}
	var marked int64
	if err := f.db.QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, id).Scan(&marked); err != nil || marked != now {
		t.Fatalf("new orphan marker = %d, %v", marked, err)
	}
}

func TestOrphanSecretMaintenanceRechecksAfterProbe(t *testing.T) {
	for _, cancelWork := range []bool{false, true} {
		name := "new_reference"
		if cancelWork {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			f := newClaimFixture(t)
			user := f.seedUser("orphan-reference", false)
			id := seedUnreferencedRecoverySecret(t, f, 123, nil)
			var references int
			if err := f.store.ReadDB().QueryRow(`SELECT count(*) FROM secret_live_references`).Scan(&references); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			held, err := f.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			waits := f.db.Stats().WaitCount
			done := make(chan error, 1)
			go func() {
				report, err := f.service.MaintainOrphanSecretsAt(ctx, 5_000, 10, time.Time{})
				if err == nil && report != (MaintenanceReport{}) {
					err = fmt.Errorf("maintenance changed referenced secret: %+v", report)
				}
				done <- err
			}()
			awaitCompletionCondition(t, func() bool { return f.db.Stats().WaitCount > waits })
			if f.store.ReadDB().Stats().InUse != 0 {
				t.Fatal("maintenance retained reader while waiting for writer")
			}
			if cancelWork {
				cancel()
			} else {
				// The reader saw an orphan, but a current credential now owns it.
				tx, err := held.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.ExecContext(ctx, `INSERT INTO credentials(id,user_id,current_version_id,created_at,updated_at)
VALUES(1,?,1,1000,1000)`, user); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO credential_versions(id,credential_id,version,secret_ref_id,created_at)
VALUES(1,1,1,?,1000)`, id); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			err = awaitCompletionResult(t, done)
			if cancelWork && !errors.Is(err, context.Canceled) || !cancelWork && err != nil {
				t.Fatalf("maintenance result = %v", err)
			}
			var marker sql.NullInt64
			if err := f.db.QueryRow(`SELECT orphaned_at FROM endpoint_key_secrets WHERE id=?`, id).Scan(&marker); err != nil || marker.Valid {
				t.Fatalf("protected secret marker = %+v, %v", marker, err)
			}
			if cancelWork {
				report, err := f.service.MaintainOrphanSecretsAt(t.Context(), 5_000, 10, time.Time{})
				if err != nil || report.Marked != 1 || report.Deleted != 0 || report.More {
					t.Fatalf("retry after cancellation = %+v, %v", report, err)
				}
			}
		})
	}
}

func BenchmarkEmptyOrphanSecretMaintenance(b *testing.B) {
	for _, count := range []int{100, 1_000} {
		b.Run(fmt.Sprintf("live_%d", count), func(b *testing.B) {
			f := newClaimFixture(b)
			user := f.seedUser("live-secret-scale", false)
			tx, err := f.db.BeginTx(b.Context(), nil)
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`WITH RECURSIVE ids(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM ids WHERE n<?)
INSERT INTO endpoint_key_secrets(id,context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
SELECT n,CAST(printf('%016d',n) AS BLOB),'https://live.example.test/v1','openai-compatible',printf('envelope-%d',n),1000 FROM ids`, count); err != nil {
				b.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO credentials(id,user_id,current_version_id,created_at,updated_at)
SELECT id,?,id,1000,1000 FROM endpoint_key_secrets`, user); err != nil {
				b.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO credential_versions(id,credential_id,version,secret_ref_id,created_at)
SELECT id,id,1,id,1000 FROM endpoint_key_secrets`); err != nil {
				b.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			for _, pool := range []*sql.DB{f.db, f.store.ReadDB()} {
				var live int
				if err := pool.QueryRow(`SELECT count(*) FROM secret_live_references`).Scan(&live); err != nil || live != count {
					b.Fatalf("warm live references = %d, %v", live, err)
				}
			}
			if report, err := f.service.MaintainOrphanSecretsAt(b.Context(), 5_000, MaxRecoveryBatch, time.Time{}); err != nil || report != (MaintenanceReport{}) {
				b.Fatalf("warm empty maintenance = %+v, %v", report, err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				report, err := f.service.MaintainOrphanSecretsAt(b.Context(), 5_000, MaxRecoveryBatch, time.Time{})
				if err != nil || report != (MaintenanceReport{}) {
					b.Fatalf("empty maintenance = %+v, %v", report, err)
				}
			}
		})
	}
}
