package lakenotes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
)

func collectionFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.enable(t)
	f.profile(t)
	f.service.reward = func(ctx context.Context, tx *sql.Tx, user int64, location string, at int64) error {
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT donation_credit_mag FROM users WHERE id=?", user).Scan(&raw); err != nil {
			return err
		}
		value, err := db.DecodeU128(raw)
		if err != nil {
			return err
		}
		next, err := db.U128FromBig(new(big.Int).Add(value.Big(), big.NewInt(50000)))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE users SET donation_credit_mag=? WHERE id=?", db.EncodeU128(next), user)
		return err
	}
	return f
}

func completeCollection(t *testing.T, f *fixture, location string) {
	t.Helper()
	f.tx(t, func(tx *sql.Tx) {
		row, err := profileTx(f.ctx(f.user), tx, f.user, true, testNow)
		if err != nil {
			t.Fatal(err)
		}
		var catalog rules.Catalog
		if err = json.Unmarshal(rules.CatalogJSON(), &catalog); err != nil {
			t.Fatal(err)
		}
		for _, fish := range catalog.Fish {
			if fish.Location == location {
				row.profile.Records[fish.Kind] = rules.Record{Caught: "1", MaxLength: fish.Length[0], BestQuality: 0, PerfectCount: "0"}
			}
		}
		if err = saveProfileTx(f.ctx(f.user), tx, f.user, &row, testNow); err != nil {
			t.Fatal(err)
		}
	})
}

func donationValue(t *testing.T, f *fixture) string {
	t.Helper()
	var raw []byte
	if err := f.database.QueryRow("SELECT donation_credit_mag FROM users WHERE id=?", f.user).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	v, err := db.DecodeU128(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v.Decimal()
}

func TestCollectionClaimPermanentAtomicAndAccountScoped(t *testing.T) {
	f := collectionFixture(t)
	completeCollection(t, f, "lake")
	before := f.profile(t)
	in := CollectionInput{Location: "lake"}
	result, err := f.service.ClaimCollection(f.ctx(f.user), f.user, testKey(900), in)
	if err != nil || result.Value.Profile.CollectionClaims["lake"] != testNow || donationValue(t, f) != "50000" {
		t.Fatalf("claim: %+v %v", result, err)
	}
	if result.Value.Wallet != before.Wallet || result.Value.Profile.Coins != before.Profile.Coins || result.Value.Profile.XP != before.Profile.XP {
		t.Fatal("collection awarded spendable currency or fishing XP")
	}
	replay, err := f.service.ClaimCollection(f.ctx(f.user), f.user, testKey(900), in)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err = f.service.ClaimCollection(f.ctx(f.user), f.user, testKey(901), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err = f.service.ClaimCollection(f.ctx(f.other), f.user, testKey(902), in); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("other account: %v", err)
	}
	if _, err = f.service.ClaimCollection(f.ctx(f.other), f.other, testKey(903), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("incomplete other: %v", err)
	}
	if donationValue(t, f) != "50000" {
		t.Fatal("reward duplicated")
	}
	f.tx(t, func(tx *sql.Tx) {
		exported, e := f.service.ExportUserTx(f.ctx(f.user), tx, f.user, 100)
		if e != nil || exported.Profile.CollectionClaims["lake"] != testNow {
			t.Fatalf("export: %v", e)
		}
	})
	if _, err = f.service.Retain(context.Background(), testNow+86400*200, 100, time.Second); err != nil {
		t.Fatal(err)
	}
	if f.profile(t).Profile.CollectionClaims["lake"] != testNow {
		t.Fatal("retention removed permanent claim")
	}
	f.tx(t, func(tx *sql.Tx) {
		if _, e := f.service.PrepareDeleteTx(f.ctx(f.user), tx, f.user, testNow); e != nil {
			t.Fatal(e)
		}
	})
	var count int
	if err = f.database.QueryRow("SELECT count(*) FROM lake_notes_profiles WHERE user_id=?", f.user).Scan(&count); err != nil || count != 0 {
		t.Fatalf("delete: %d %v", count, err)
	}
}

func TestCollectionRollbackAndQualification(t *testing.T) {
	f := collectionFixture(t)
	in := CollectionInput{Location: "lake"}
	if _, err := f.service.ClaimCollection(f.ctx(f.user), f.user, testKey(910), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("incomplete: %v", err)
	}
	completeCollection(t, f, "lake")
	before := f.profile(t)
	failed := errors.New("activity failed")
	f.service.activity = func(context.Context, *sql.Tx, int64, int64) error { return failed }
	if _, err := f.service.ClaimCollection(f.ctx(f.user), f.user, testKey(911), in); !errors.Is(err, failed) {
		t.Fatalf("rollback: %v", err)
	}
	if donationValue(t, f) != "0" || f.profile(t).Revision != before.Revision || len(f.profile(t).Profile.CollectionClaims) != 0 {
		t.Fatal("transaction leaked reward or claim")
	}
	f.service.activity = nil
	// Use the normal configuration API so both game settings and revisions are valid.
	wire := before.Settings.Wire
	wire.Enabled = false
	if _, err := f.configure(wire); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ClaimCollection(f.ctx(f.user), f.user, testKey(911), in); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
	f.enable(t)
	if _, err := f.service.ClaimCollection(f.ctx(f.user), f.user, testKey(911), in); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionConcurrentDifferentKeys(t *testing.T) {
	f := collectionFixture(t)
	completeCollection(t, f, "lake")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.service.ClaimCollection(f.ctx(f.user), f.user, testKey(920+i), CollectionInput{Location: "lake"})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("concurrent: %v", err)
		}
	}
	if success != 1 || donationValue(t, f) != "50000" {
		t.Fatalf("successes=%d donation=%s", success, donationValue(t, f))
	}
}
