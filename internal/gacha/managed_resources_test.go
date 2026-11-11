package gacha

import (
	"reflect"
	"testing"
)

func TestManagedResourcesPreserveProgressAndAvoidDuplicateConversion(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "INSERT INTO gacha_pity(user_id,group_id,sr,ssr,sr_limit,ssr_limit,sr_featured,ssr_featured) VALUES(?,'permanent',3,40,10,60,0,0)", f.user)
	f.exec(t, "UPDATE gacha_profiles SET monthly_permanent=4,monthly_limited=2 WHERE user_id=?", f.user)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before, _, err := readProfile(t.Context(), tx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	pity, err := readPity(t.Context(), tx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	for _, quantity := range []string{"1", "1", "0", "1"} {
		if err := SetManagedResourceTx(t.Context(), tx, f.user, f.at.Load(), "gacha:card:l-chatgpt", quantity); err != nil {
			t.Fatal(err)
		}
	}
	after, _, err := readProfile(t.Context(), tx, f.user)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("ownership adjustment changed wallet or progression", before, after, err)
	}
	if err := SetManagedResourceTx(t.Context(), tx, f.user, f.at.Load(), "gacha:permanent", "7"); err != nil {
		t.Fatal(err)
	}
	after, _, err = readProfile(t.Context(), tx, f.user)
	before.Balance[0] = 7
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("ticket adjustment changed unrelated progress", err)
	}
	current, err := readPity(t.Context(), tx, f.user)
	if err != nil || !reflect.DeepEqual(current, pity) {
		t.Fatal("pity changed", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
