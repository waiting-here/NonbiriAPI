package clientguard

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const testNow int64 = 1790000640

type mockRetirement struct{ committed, aborted *int }

func (r mockRetirement) Commit() bool { *r.committed++; return true }
func (r mockRetirement) Abort() bool  { *r.aborted++; return true }

type guardFixture struct {
	t                                        *testing.T
	store                                    *db.Store
	service                                  *Service
	user                                     int64
	gateCalls, committed, aborted            int
	gameCalls, gamePublished, gameRolledBack int
	beforeGate                               func()
}

func newGuardFixture(t *testing.T) *guardFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "guard.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &guardFixture{t: t, store: store}
	claims, err := claim.New(claim.Dependencies{DB: store.DB(), Secrets: vault})
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.InitializeUsageTotals(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.exec(`CREATE TABLE guard_game_cancels(user_id INTEGER PRIMARY KEY)`)
	f.service, err = New(Config{
		Database: store.DB(), Rejections: claims,
		BeginUserRetirement: func(context.Context, int64) (Retirement, error) {
			f.gateCalls++
			if f.beforeGate != nil {
				f.beforeGate()
			}
			return mockRetirement{&f.committed, &f.aborted}, nil
		},
		CancelUserGamesTx: func(ctx context.Context, tx *sql.Tx, user int64, reason string, now int64) (func(bool), error) {
			f.gameCalls++
			if reason != "account_unavailable" || now != testNow {
				return nil, ErrInvalid
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO guard_game_cancels(user_id) VALUES(?)`, user); err != nil {
				return nil, err
			}
			return func(commit bool) {
				if commit {
					f.gamePublished++
				} else {
					f.gameRolledBack++
				}
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.user = f.makeUser()
	return f
}

func (f *guardFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.store.DB().Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *guardFixture) scalar(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.store.DB().QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
func (f *guardFixture) makeUser() int64 {
	f.t.Helper()
	zero := db.EncodeU128(db.U128{})
	result, err := f.store.DB().Exec(`INSERT INTO users(username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('Guard fixture',?,?,?,?,?,?,?,?,?,?)`, zero, zero, zero, zero, zero, zero, zero, zero, testNow, testNow)
	if err != nil {
		f.t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		f.t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("guard-key"))
	f.exec(`INSERT INTO caller_keys(user_id,generation,key_hash,key_created_at,updated_at) VALUES(?,1,?,?,?)`, id, hash[:], testNow, testNow)
	return id
}
func (f *guardFixture) rule(name, pattern string, enabled bool, duration *int64) string {
	f.t.Helper()
	id, err := db.GenerateOpaqueID("rsk_")
	if err != nil {
		f.t.Fatal(err)
	}
	conditions := fmt.Sprintf(`[{"field":"user_agent","operator":"prefix","value":%q,"case_sensitive":false}]`, pattern)
	f.exec(`INSERT INTO risk_client_rules(id,name,status,enabled,revision,conditions_json,evidence_note,evidence_url,created_by_role,updated_by_role,created_at,updated_at) VALUES(?,?,'suspected',1,1,?,'','','admin','admin',?,?)`, id, name, conditions, testNow, testNow)
	f.exec(`INSERT INTO client_rule_auto_bans(rule_id,enabled,duration_seconds,revision,updated_at) VALUES(?,?,?,1,?)`, id, enabled, duration, testNow)
	return id
}
func (f *guardFixture) context(kind, ua string) context.Context {
	f.t.Helper()
	ctx, _, err := requestattempt.New(context.Background(), f.user, "POST", "/v1/chat/completions")
	if err != nil {
		f.t.Fatal(err)
	}
	requestattempt.Classify(ctx, kind)
	return observability.WithSource(ctx, observability.Source{UserAgent: ua})
}

func TestFreshCharityMatchIsAtomicZeroCostAndIdempotent(t *testing.T) {
	f := newGuardFixture(t)
	long := int64(3600)
	f.rule("short", "Client/", true, &long)
	f.rule("permanent", "Client/", true, nil)
	// Many ordinary rules before the binding cannot mask it.
	for i := 0; i < 120; i++ {
		f.exec(`INSERT INTO risk_client_rules(id,name,status,enabled,revision,conditions_json,evidence_note,evidence_url,created_by_role,updated_by_role,created_at,updated_at) VALUES(?,?,'suspected',1,1,'[{"field":"user_agent","operator":"equals","value":"Other","case_sensitive":false}]','','','admin','admin',?,?)`, fmt.Sprintf("ordinary-%03d", i), "other", testNow, testNow)
	}
	ctx := f.context("charity", "Client/1")
	requestID := requestattempt.CurrentID(ctx)
	decision, err := f.service.CheckCharityCall(ctx, f.user, "[公益]provider/model", testNow)
	if err != nil || !decision.Banned || decision.Until != nil || decision.Replayed {
		t.Fatalf("decision %+v %v", decision, err)
	}
	if f.gateCalls != 1 || f.committed != 1 || f.gameCalls != 1 || f.gamePublished != 1 || f.gameRolledBack != 0 {
		t.Fatalf("gate/game %d %d %d %d %d", f.gateCalls, f.committed, f.gameCalls, f.gamePublished, f.gameRolledBack)
	}
	if f.scalar(`SELECT count(*) FROM client_rule_ban_receipts WHERE request_id=? AND json_array_length(rules_json)=2`, requestID) != 1 {
		t.Fatal("missing bounded receipt")
	}
	if f.scalar(`SELECT count(*) FROM logical_requests WHERE id=? AND route_kind='charity_chat_completions' AND rejection_stage='preflight' AND rejection_reason='forbidden' AND caller_status=403 AND account_reserved_milli=0`, requestID) != 1 {
		t.Fatal("missing zero-cost pre-dispatch refusal")
	}
	if f.scalar(`SELECT count(*) FROM dispatch_claims`) != 0 || f.scalar(`SELECT count(*) FROM credit_entries`) != 0 {
		t.Fatal("upstream or fee effect on first hit")
	}
	var reason string
	if err := f.store.DB().QueryRow(`SELECT banned_reason FROM users WHERE id=?`, f.user).Scan(&reason); err != nil || reason != "识别到违规第三方客户端特征：permanent、short" {
		t.Fatalf("reason %q %v", reason, err)
	}
	replay, err := f.service.CheckCharityCall(ctx, f.user, "[公益]provider/model", testNow)
	if err != nil || !replay.Banned || !replay.Replayed || f.gameCalls != 1 || f.gateCalls != 2 {
		t.Fatalf("replay %+v %v", replay, err)
	}
	if f.scalar(`SELECT count(*) FROM request_logs WHERE logical_request_id=?`, requestID) != 1 {
		t.Fatal("duplicate rejection")
	}
	deleted, err := f.service.CleanupReceiptsBatch(context.Background(), testNow+receiptLifetime-1, 100)
	if err != nil || deleted != 0 {
		t.Fatalf("early cleanup %d %v", deleted, err)
	}
	deleted, err = f.service.CleanupReceiptsBatch(context.Background(), testNow+receiptLifetime, 100)
	if err != nil || deleted != 1 {
		t.Fatalf("expiry cleanup %d %v", deleted, err)
	}
}

func TestRulesRecheckedAfterGateAndNoMatchDoesNotRetire(t *testing.T) {
	f := newGuardFixture(t)
	id := f.rule("race", "Client/", true, nil)
	allow, err := f.service.CheckCharityCall(f.context("charity", "Other/1"), f.user, "[公益]provider/model", testNow)
	if err != nil || allow.Banned || f.gateCalls != 0 {
		t.Fatalf("no match %+v %v", allow, err)
	}
	f.beforeGate = func() {
		f.exec(`UPDATE risk_client_rules SET revision=revision+1,updated_at=updated_at+1 WHERE id=?`, id)
		f.exec(`UPDATE client_rule_auto_bans SET enabled=0,revision=revision+1,updated_at=updated_at+1 WHERE rule_id=?`, id)
	}
	decision, err := f.service.CheckCharityCall(f.context("charity", "Client/1"), f.user, "[公益]provider/model", testNow)
	if err != nil || decision.Banned || f.gameCalls != 0 || f.committed != 0 || f.aborted != 1 {
		t.Fatalf("changed rule %+v %v", decision, err)
	}
	if f.scalar(`SELECT is_banned FROM users WHERE id=?`, f.user) != 0 {
		t.Fatal("stale match banned account")
	}
	for _, kind := range []string{"self", "discovery"} {
		if _, err := f.service.CheckCharityCall(f.context(kind, "Client/1"), f.user, "[公益]provider/model", testNow); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s entered penalty path: %v", kind, err)
		}
	}
}

func TestPriorManualBanAndFailedGameHookPreserveState(t *testing.T) {
	f := newGuardFixture(t)
	duration := int64(60)
	f.rule("finite", "Client/", true, &duration)
	f.exec(`UPDATE users SET is_banned=1,banned_until=?,banned_reason='Manual review',auto_banned=0 WHERE id=?`, testNow+3600, f.user)
	f.exec(`CREATE TRIGGER guard_fail_receipt BEFORE INSERT ON client_rule_ban_receipts BEGIN SELECT RAISE(ABORT,'injected'); END`)
	_, err := f.service.CheckCharityCall(f.context("charity", "Client/1"), f.user, "[公益]provider/model", testNow)
	if err == nil || f.committed != 0 || f.gameRolledBack != 1 || f.scalar(`SELECT count(*) FROM guard_game_cancels`) != 0 || f.scalar(`SELECT count(*) FROM request_logs`) != 0 {
		t.Fatalf("rollback failure %v", err)
	}
	var reason string
	if err := f.store.DB().QueryRow(`SELECT banned_reason FROM users WHERE id=?`, f.user).Scan(&reason); err != nil || reason != "Manual review" {
		t.Fatal("manual evidence changed on rollback")
	}
	f.exec(`DROP TRIGGER guard_fail_receipt`)
	decision, err := f.service.CheckCharityCall(f.context("charity", "Client/1"), f.user, "[公益]provider/model", testNow)
	if err != nil || decision.Until == nil || *decision.Until != testNow+3600 {
		t.Fatalf("manual maximum %+v %v", decision, err)
	}
	if err := f.store.DB().QueryRow(`SELECT banned_reason FROM users WHERE id=?`, f.user).Scan(&reason); err != nil || reason != "识别到违规第三方客户端特征：finite\n既有封禁原因：Manual review" {
		t.Fatalf("lost evidence %q %v", reason, err)
	}
}

func TestIdentityAndManualRestrictionGatePrecedesPenaltyTransaction(t *testing.T) {
	f := newGuardFixture(t)
	f.rule("gate", "Client/", true, nil)
	key := sha256.Sum256([]byte("stable identity"))
	gate, err := lifecyclegate.New(lifecyclegate.Config{IdentityResolver: func(context.Context, int64) ([32]byte, error) { return key, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gate.Close() })
	admitted, release, err := gate.Admit(context.Background(), f.user, "key", func(context.Context, int64, string) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, _, err := requestattempt.New(admitted, f.user, "POST", "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	requestattempt.Classify(ctx, "charity")
	ctx = observability.WithSource(ctx, observability.Source{UserAgent: "Client/1"})
	barrier := make(chan struct{})
	resume := make(chan struct{})
	f.service.config.BeginUserRetirement = func(ctx context.Context, user int64) (Retirement, error) {
		retirement, err := gate.BeginUserRetirementExcludingContext(ctx, user)
		if err != nil {
			return nil, err
		}
		close(barrier)
		<-resume
		return retirement, nil
	}
	type outcome struct {
		decision Decision
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		d, e := f.service.CheckCharityCall(ctx, f.user, "[公益]provider/model", testNow)
		done <- outcome{d, e}
	}()
	<-barrier
	if _, err := gate.BeginIdentityChange(context.Background(), key); !errors.Is(err, lifecyclegate.ErrRetiring) {
		t.Fatalf("identity change bypassed ban barrier: %v", err)
	}
	if _, err := gate.BeginUserRetirement(f.user); !errors.Is(err, lifecyclegate.ErrRetiring) {
		t.Fatalf("manual restriction bypassed ban barrier: %v", err)
	}
	close(resume)
	result := <-done
	if result.err != nil || !result.decision.Banned {
		t.Fatalf("penalty %+v %v", result.decision, result.err)
	}
}

func TestAccountRemovedBeforePenaltyTransactionCannotBeRebanned(t *testing.T) {
	f := newGuardFixture(t)
	f.rule("retired", "Client/", true, nil)
	old := f.user
	f.beforeGate = func() { f.exec(`DELETE FROM users WHERE id=?`, old) }
	_, err := f.service.CheckCharityCall(f.context("charity", "Client/1"), old, "[公益]provider/model", testNow)
	if err == nil || f.gameCalls != 0 || f.scalar(`SELECT count(*) FROM client_rule_ban_receipts`) != 0 {
		t.Fatalf("deleted account processed: %v", err)
	}
	f.makeUser()
	if f.scalar(`SELECT count(*) FROM users WHERE id=?`, old) != 0 {
		t.Fatal("retired account revived")
	}
}

func TestHundredRuleEvidenceFitsManagementReasonBudget(t *testing.T) {
	f := newGuardFixture(t)
	for i := 0; i < 100; i++ {
		f.rule(fmt.Sprintf("%03d-%s", i, strings.Repeat("名称", 58)), "Client/", true, nil)
	}
	ctx := f.context("charity", "Client/1")
	decision, err := f.service.CheckCharityCall(ctx, f.user, "[公益]provider/model", testNow)
	if err != nil || !decision.Banned {
		t.Fatalf("maximum evidence %+v %v", decision, err)
	}
	var reason string
	if err := f.store.DB().QueryRow(`SELECT banned_reason FROM users WHERE id=?`, f.user).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if len(reason) > 4096 || utf8.RuneCountInString(reason) > 1024 || !strings.HasPrefix(reason, "识别到违规第三方客户端特征：000-") || !strings.Contains(reason, "（另有") {
		t.Fatalf("reason exceeds management budget: %d bytes, %d runes", len(reason), utf8.RuneCountInString(reason))
	}
	if f.scalar(`SELECT json_array_length(rules_json) FROM client_rule_ban_receipts WHERE request_id=?`, requestattempt.CurrentID(ctx)) != 100 {
		t.Fatal("full rule evidence missing from receipt")
	}
}

func TestAutomaticBanReasonUsesFinalRuleNameAndKeepsReceipt(t *testing.T) {
	f := newGuardFixture(t)
	id := f.rule("Previous name", "Tavo/", true, nil)
	f.beforeGate = func() {
		f.exec(`UPDATE risk_client_rules SET name='Tavo',revision=revision+1 WHERE id=?`, id)
		f.exec(`UPDATE client_rule_auto_bans SET revision=revision+1 WHERE rule_id=?`, id)
	}
	ctx := f.context("charity", "Tavo/secret-not-a-reason")
	decision, err := f.service.CheckCharityCall(ctx, f.user, "[公益]provider/model", testNow)
	if err != nil || !decision.Banned {
		t.Fatalf("client penalty %+v %v", decision, err)
	}
	var reason, refs string
	if err := f.store.DB().QueryRow(`SELECT banned_reason FROM users WHERE id=?`, f.user).Scan(&reason); err != nil || reason != "识别到违规第三方客户端特征：Tavo" {
		t.Fatalf("readable current reason %q %v", reason, err)
	}
	if err := f.store.DB().QueryRow(`SELECT rules_json FROM client_rule_ban_receipts WHERE request_id=?`, requestattempt.CurrentID(ctx)).Scan(&refs); err != nil || refs != fmt.Sprintf(`[{"id":%q,"revision":2}]`, id) {
		t.Fatalf("complete revision receipt %q %v", refs, err)
	}
	f.beforeGate = nil
	f.exec(`UPDATE risk_client_rules SET name='Renamed later',revision=revision+1 WHERE id=?`, id)
	f.exec(`UPDATE client_rule_auto_bans SET revision=revision+1 WHERE rule_id=?`, id)
	replay, err := f.service.CheckCharityCall(ctx, f.user, "[公益]provider/model", testNow)
	if err != nil || !replay.Replayed || f.gameCalls != 1 {
		t.Fatalf("duplicate penalty %+v %v", replay, err)
	}
	if err := f.store.DB().QueryRow(`SELECT banned_reason FROM users WHERE id=?`, f.user).Scan(&reason); err != nil || reason != "识别到违规第三方客户端特征：Tavo" {
		t.Fatalf("replay rewrote historical reason %q %v", reason, err)
	}
}
