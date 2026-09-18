package adminusers

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
)

func TestBanDuelCancellationSharesTransactionAndFinalizesOnce(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			f := newAdminUsersFixture(t)
			user := f.seedUser("cancel-participant", false)
			f.addSession(user, "participant-session")
			if _, err := f.store.DB().Exec(`CREATE TABLE cancellation_probe(value INTEGER NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			calls, committed, aborted := 0, 0, 0
			f.service.cancelUserDuelsTx = func(ctx context.Context, tx *sql.Tx, id int64, reason string, now int64) (func(bool), error) {
				calls++
				var banned int
				if err := tx.QueryRowContext(ctx, `SELECT is_banned FROM users WHERE id=?`, id).Scan(&banned); err != nil {
					return nil, err
				}
				if id != user || reason != "account_unavailable" || now != adminUsersTestNow || banned != 0 {
					t.Fatal("cancellation did not precede ban")
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO cancellation_probe VALUES(1)`); err != nil {
					return nil, err
				}
				return func(ok bool) {
					if ok {
						committed++
					} else {
						aborted++
					}
				}, nil
			}
			if rollback {
				if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_revoke BEFORE UPDATE ON caller_keys BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			body := `{"expected_revision":"1","reason":"policy","duration_seconds":60}`
			key := "cancellation-request-0001"
			response := f.request(http.MethodPost, routeBan, "https://admin.example/admin/api/users/1/ban", body, user, key)
			var rows, banned int
			if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM cancellation_probe`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if err := f.store.DB().QueryRow(`SELECT is_banned FROM users WHERE id=?`, user).Scan(&banned); err != nil {
				t.Fatal(err)
			}
			if rollback {
				if response.Code != 500 || calls != 1 || committed != 0 || aborted != 1 || rows != 0 || banned != 0 {
					t.Fatalf("rollback: status=%d calls=%d commit=%d abort=%d rows=%d banned=%d", response.Code, calls, committed, aborted, rows, banned)
				}
			} else {
				if response.Code != 204 || calls != 1 || committed != 1 || aborted != 0 || rows != 1 || banned != 1 {
					t.Fatal("ban and cancellation did not commit together", response.Code)
				}
				if replay := f.request(http.MethodPost, routeBan, "https://admin.example/admin/api/users/1/ban", body, user, key); replay.Code != 204 || calls != 1 || committed != 1 {
					t.Fatal("replay repeated cancellation")
				}
			}
		})
	}
}
