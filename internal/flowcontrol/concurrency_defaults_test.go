package flowcontrol

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestConcurrencySiteDefaultsHotUpdateOverridesAndInFlightRelease(t *testing.T) {
	ctx := context.Background()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "concurrency.db")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	zero := db.EncodeU128(db.U128{})
	result, err := store.DB().Exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('777','fixture',?,?,?,?,?,?,?,?,1,1)`, zero, zero, zero, zero, zero, zero, zero, zero)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	checkLimits := func(heavy, light int) {
		t.Helper()
		limits, err := store.GetUserAdmissionLimits(ctx, userID)
		if err != nil || limits.ConcurrencyLimit != heavy || limits.LightConcurrencyLimit != light {
			t.Fatalf("effective limits=%+v err=%v, want %d/%d", limits, err, heavy, light)
		}
	}
	setDefaults := func(heavy, light string) {
		t.Helper()
		exec(`INSERT INTO site_config(key,value,updated_at) VALUES('default_concurrency_per_user',?,0),('default_light_concurrency_per_user',?,0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, heavy, light)
	}
	checkLimits(5, 15)
	setDefaults("2", "2")
	c := newTestController(t, generousRPMConfig(), DBUserLimitResolver(store), nil)
	admit := func(operation contract.Operation, charity bool) *Reservation {
		t.Helper()
		r, _, err := c.AdmitOperation(ctx, userID, operation, charity)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Release() })
		return r
	}
	deny := func(operation contract.Operation, charity bool) {
		t.Helper()
		if r, _, err := c.AdmitOperation(ctx, userID, operation, charity); !errors.Is(err, ErrConcurrencyLimited) {
			if r != nil {
				r.Release()
			}
			t.Fatalf("full pool admitted request: %v", err)
		}
	}
	heavyA := admit(contract.OperationChatCompletions, false)
	heavyB := admit(contract.OperationChatCompletions, true)
	lightA := admit(contract.OperationEmbeddings, false)
	lightB := admit(contract.OperationRerank, true)
	deny(contract.OperationChatCompletions, false)
	deny(contract.OperationEmbeddings, true)

	setDefaults("1", "3")
	checkLimits(1, 3)
	if !heavyA.Active() || !heavyB.Active() || !lightA.Active() || !lightB.Active() {
		t.Fatal("site update cancelled an active request")
	}
	deny(contract.OperationChatCompletions, true)
	lightC := admit(contract.OperationDecisions, false)
	deny(contract.OperationEmbeddings, false)
	heavyA.Release()
	deny(contract.OperationChatCompletions, false)
	heavyB.Release()
	heavyC := admit(contract.OperationChatCompletions, true)
	heavyC.Release()
	lightA.Release()
	lightB.Release()
	lightC.Release()

	exec("UPDATE users SET concurrency_limit=3,light_concurrency_limit=1 WHERE id=?", userID)
	setDefaults("1", "4")
	checkLimits(3, 1)
	a := admit(contract.OperationChatCompletions, false)
	b := admit(contract.OperationChatCompletions, true)
	d := admit(contract.OperationChatCompletions, false)
	l := admit(contract.OperationEmbeddings, true)
	deny(contract.OperationChatCompletions, true)
	deny(contract.OperationRerank, false)
	for _, r := range []*Reservation{a, b, d, l} {
		r.Release()
	}

	exec("UPDATE users SET concurrency_limit=NULL,light_concurrency_limit=NULL WHERE id=?", userID)
	checkLimits(1, 4)
	h := admit(contract.OperationChatCompletions, false)
	deny(contract.OperationChatCompletions, true)
	for range 4 {
		admit(contract.OperationEmbeddings, false)
	}
	deny(contract.OperationRerank, true)
	h.Release()
}
