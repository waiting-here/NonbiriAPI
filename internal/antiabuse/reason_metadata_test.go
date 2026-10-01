package antiabuse

import (
	"context"
	"testing"
)

func TestRestrictionReplacementRetiresOnlyCurrentBanReasonMetadata(t *testing.T) {
	for _, test := range []struct {
		name         string
		ban, suspend int64
		rollback     bool
		want         int64
	}{
		{"suspension_preserves_ban", 0, 60, false, 1},
		{"ban_replaces_reason", 60, 0, false, 0},
		{"failed_ban_preserves_reason", 60, 0, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newAbuseFixture(t)
			user, now := f.user(), f.clock.Load()
			if _, err := f.store.DB().Exec(`INSERT INTO automatic_reason_metadata(owner_kind,owner_id,kind,schema_version,params_json,manual_text) VALUES('user_ban',CAST(? AS TEXT),'client_rules',1,'{}','Original manual fact')`, user); err != nil {
				t.Fatal(err)
			}
			if test.rollback {
				if _, err := f.store.DB().Exec(`CREATE TRIGGER fail_metadata_ban BEFORE UPDATE ON caller_keys BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := f.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var revision []byte
			if err = tx.QueryRow(`SELECT revision FROM users WHERE id=?`, user).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			err = applyRestrictions(context.Background(), tx, user, now, revision, test.ban, test.suspend)
			if test.rollback {
				if err == nil {
					t.Fatal("injected failure did not abort replacement")
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			if count := f.scalar(`SELECT count(*) FROM automatic_reason_metadata WHERE owner_kind='user_ban' AND owner_id=CAST(? AS TEXT)`, user); count != test.want {
				t.Fatal("incorrect current reason ownership", count, test.want)
			}
		})
	}
}
