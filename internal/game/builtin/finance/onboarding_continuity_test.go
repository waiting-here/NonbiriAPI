package finance

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func newOnboardingFixture(t *testing.T) blackjackFinanceFixture {
	t.Helper()
	file := filepath.Join(t.TempDir(), "onboarding.sqlite")
	dbfixture.Materialize(t, file)
	database, err := sql.Open("sqlite", file+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	f := blackjackWallet(t, database, 1000, 0)
	return f
}

func onboardingTestUser(t *testing.T, f blackjackFinanceFixture, discord string) (int64, int64) {
	t.Helper()
	zero := db.EncodeU128(db.U128{})
	result := f.exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, discord, "player", zero, zero, zero, zero, zero, zero, zero, zero, 100, 100)
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	bindFinanceUser(t, f.database, f.tx, userID)
	wallet, err := ledger.CreateUserAccount(f.ctx, f.tx, userID, 100)
	if err != nil {
		t.Fatal(err)
	}
	return userID, wallet.ID
}

func onboardingTestEntry(t *testing.T, f blackjackFinanceFixture, userID int64) onboardingParent {
	t.Helper()
	id := f.id("bjq_")
	f.exec(`INSERT INTO game_blackjack_entries(id,user_id,state,stake_milli,platform_bp,welfare_bp,thursday_bp,created_at) VALUES(?,?,'waiting',1000,100,100,100,100)`, id, userID)
	return onboardingParent{column: "blackjack_entry_id", id: id}
}

func onboardingTestCount(t *testing.T, f blackjackFinanceFixture, table string) int {
	t.Helper()
	var count int
	if err := f.tx.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestOnboardingContinuityPendingCompletionAndAccountTurnover(t *testing.T) {
	f := newOnboardingFixture(t)
	port := onboarding{config.Descriptor()}
	firstParent := onboardingTestEntry(t, f, f.user)
	firstWallet := f.wallets.General
	var providerID string
	if err := f.tx.QueryRow(`SELECT discord_id FROM users WHERE id=?`, f.user).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	// Two pending accounts share one trusted test identity. A hold alone must
	// not consume the lifetime qualification.
	rival, rivalWallet := onboardingTestUser(t, f, "pending-rival")
	f.exec(`UPDATE user_continuity_identities SET identity_key=(SELECT identity_key FROM user_continuity_identities WHERE user_id=?) WHERE user_id=?`, f.user, rival)
	rivalParent := onboardingTestEntry(t, f, rival)
	for _, pair := range []struct {
		user   int64
		parent onboardingParent
	}{{f.user, firstParent}, {rival, rivalParent}} {
		if err := port.reserve(f.ctx, f.tx, pair.user, "complete", pair.parent, 110); err != nil {
			t.Fatal(err)
		}
	}
	if got := onboardingTestCount(t, f, "identity_continuity_facts"); got != 0 {
		t.Fatalf("pending hold consumed eligibility: %d", got)
	}
	if got := onboardingTestCount(t, f, "game_onboarding_holds"); got != 2 {
		t.Fatalf("pending holds=%d", got)
	}
	if err := port.complete(f.ctx, f.tx, f.user, "complete", firstParent, 120); err != nil {
		t.Fatal(err)
	}
	if err := port.complete(f.ctx, f.tx, rival, "complete", rivalParent, 121); err != nil {
		t.Fatalf("duplicate completion did not release hold: %v", err)
	}
	if got := onboardingTestCount(t, f, "game_onboarding_holds"); got != 0 {
		t.Fatalf("duplicate left hold: %d", got)
	}
	if got := onboardingTestCount(t, f, "game_onboarding_completions"); got != 1 {
		t.Fatalf("completions=%d", got)
	}
	if got := onboardingTestCount(t, f, "identity_continuity_facts"); got != 1 {
		t.Fatalf("identity facts=%d", got)
	}
	var scope, window string
	var expiry sql.NullInt64
	if err := f.tx.QueryRow(`SELECT scope,window_key,expires_at FROM identity_continuity_facts WHERE kind='game_onboarding'`).Scan(&scope, &window, &expiry); err != nil || scope != "blackjack:complete" || window != "v1" || expiry.Valid {
		t.Fatalf("qualification scope=%q window=%q expiry=%v err=%v", scope, window, expiry, err)
	}
	first, err := ledger.ReadAccount(f.ctx, f.tx, firstWallet)
	if err != nil || first.Balance.Big().Int64() != 1000+1000000 {
		t.Fatalf("first wallet=%+v err=%v", first, err)
	}
	rivalBalance, err := ledger.ReadAccount(f.ctx, f.tx, rivalWallet)
	if err != nil || rivalBalance.Balance.Big().Sign() != 0 {
		t.Fatalf("duplicate wallet=%+v err=%v", rivalBalance, err)
	}

	// Turn over the provider ID, then bind a fresh user from the original ID.
	// Changing the compiled reward must not change the qualification identity.
	f.exec(`UPDATE users SET discord_id=? WHERE id=?`, "retired-"+providerID, f.user)
	next, nextWallet := onboardingTestUser(t, f, providerID)
	nextParent := onboardingTestEntry(t, f, next)
	changed := onboarding{config.Descriptor()}
	changed.module.Onboarding[0].RewardMilli++
	if err := changed.reserve(f.ctx, f.tx, next, "complete", nextParent, 130); err != nil {
		t.Fatal(err)
	}
	if err := changed.complete(f.ctx, f.tx, next, "complete", nextParent, 131); err != nil {
		t.Fatalf("repeat completion: %v", err)
	}
	if got := onboardingTestCount(t, f, "game_onboarding_completions"); got != 1 {
		t.Fatalf("re-registered account received reward: %d", got)
	}
	nextBalance, err := ledger.ReadAccount(f.ctx, f.tx, nextWallet)
	if err != nil || nextBalance.Balance.Big().Sign() != 0 {
		t.Fatalf("new account inherited balance=%+v err=%v", nextBalance, err)
	}
	if err := port.complete(f.ctx, f.tx, f.user, "complete", firstParent, 132); err != nil {
		t.Fatalf("late old-account replay: %v", err)
	}
	if got := onboardingTestCount(t, f, "game_onboarding_completions"); got != 1 {
		t.Fatalf("late old-account replay awarded again: %d", got)
	}
	other, _ := onboardingTestUser(t, f, "unrelated-discord")
	otherParent := onboardingTestEntry(t, f, other)
	if err := port.reserve(f.ctx, f.tx, other, "complete", otherParent, 140); err != nil {
		t.Fatal(err)
	}
	if err := port.complete(f.ctx, f.tx, other, "complete", otherParent, 141); err != nil {
		t.Fatalf("different Discord denied: %v", err)
	}
	if got := onboardingTestCount(t, f, "game_onboarding_completions"); got != 2 {
		t.Fatalf("different Discord completion count=%d", got)
	}
	// A stale user ID after deletion cannot transfer a late award to the new
	// account. The strict binding lookup fails before any ledger write.
	if err := port.complete(f.ctx, f.tx, 999999, "complete", firstParent, 150); !errors.Is(err, continuity.ErrNotFound) {
		t.Fatalf("late unknown account: %v", err)
	}
}

func TestOnboardingContinuityFailedCompletionRollsBackFactAndReward(t *testing.T) {
	f := newOnboardingFixture(t)
	port := onboarding{config.Descriptor()}
	parent := onboardingTestEntry(t, f, f.user)
	if err := port.reserve(f.ctx, f.tx, f.user, "complete", parent, 110); err != nil {
		t.Fatal(err)
	}
	f.exec(`SAVEPOINT onboarding_failure`)
	f.exec(`CREATE TRIGGER fail_onboarding_receipt BEFORE INSERT ON game_onboarding_completions BEGIN SELECT RAISE(ABORT,'receipt failed'); END`)
	if err := port.complete(context.Background(), f.tx, f.user, "complete", parent, 120); err == nil {
		t.Fatal("injected completion failure succeeded")
	}
	f.exec(`ROLLBACK TO onboarding_failure`)
	f.exec(`RELEASE onboarding_failure`)
	if got := onboardingTestCount(t, f, "identity_continuity_facts"); got != 0 {
		t.Fatalf("failed completion consumed fact: %d", got)
	}
	if got := onboardingTestCount(t, f, "game_onboarding_holds"); got != 1 {
		t.Fatalf("failed completion consumed hold: %d", got)
	}
	if got := onboardingTestCount(t, f, "game_onboarding_completions"); got != 0 {
		t.Fatalf("failed completion kept receipt: %d", got)
	}
	wallet, err := ledger.ReadAccount(f.ctx, f.tx, f.wallets.General)
	if err != nil || wallet.Balance.Big().Int64() != 1000 {
		t.Fatalf("failed completion changed wallet=%+v err=%v", wallet, err)
	}
	if err := port.complete(f.ctx, f.tx, f.user, "complete", parent, 121); err != nil {
		t.Fatalf("completion after rollback: %v", err)
	}
}

func TestOnboardingContinuityMissingBindingFailsClosed(t *testing.T) {
	f := newOnboardingFixture(t)
	parent := onboardingTestEntry(t, f, f.user)
	f.exec(`DELETE FROM user_continuity_identities WHERE user_id=?`, f.user)
	err := (onboarding{config.Descriptor()}).reserve(f.ctx, f.tx, f.user, "complete", parent, 110)
	if !errors.Is(err, continuity.ErrNotFound) {
		t.Fatalf("unbound reservation: %v", err)
	}
	if got := onboardingTestCount(t, f, "game_onboarding_holds"); got != 0 {
		t.Fatalf("unbound account reserved hold: %d", got)
	}
}
