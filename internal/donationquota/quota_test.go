package donationquota

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const testNow int64 = 1_700_000_000

func ptr[T any](v T) *T { return &v }
func rule(metric, limit string) RuleInput {
	return RuleInput{Mode: "reset", Interval: "5h", Alignment: ptr("first_success"), TimeZone: "UTC", Metric: metric, Limit: limit}
}
func mag(n int64) db.U128 { v, _ := db.U128FromBig(big.NewInt(n)); return v }

func newQuotaDB(t *testing.T) *sql.DB {
	t.Helper()
	vault, err := secret.New(bytes.Repeat([]byte{0x31}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "quota.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	q := store.DB()
	// Counter tests use deidentified durable parents; admission/ownership are
	// tested separately through the donation and claim services.
	_, err = q.Exec(`INSERT INTO donations(id,status,revision,description,review_note,reviewed_by_role,created_at,updated_at) VALUES(1,'approved',1,'','','',?,?)`, testNow, testNow)
	if err != nil {
		t.Fatal("donation fixture", err)
	}
	_, err = q.Exec(`INSERT INTO donation_keys(id,donation_id,source_endpoint_key_id,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,failure_streak,streak_generation,next_claim_seq,next_fold_seq,enabled,created_at,updated_at,ended_at,ended_reason,report_match_until,report_fingerprint)
VALUES(1,1,1,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,zeroblob(16),zeroblob(16),0,?,?,?,'terminated',?,zeroblob(32))`, db.EncodeU128(mag(1)), testNow, testNow, testNow, testNow+7776000)
	if err != nil {
		t.Fatal("key fixture", err)
	}
	_, err = q.Exec(`INSERT INTO endpoint_key_secrets(id,context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(1,zeroblob(16),'https://example.test/v1','openai-compatible','fixture-envelope',?)`, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func transaction(t *testing.T, q *sql.DB, f func(*sql.Tx) error) error {
	t.Helper()
	tx, err := q.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func replace(t *testing.T, q *sql.DB, at int64, rules ...RuleInput) []RuleView {
	t.Helper()
	if err := transaction(t, q, func(tx *sql.Tx) error { return Replace(context.Background(), tx, 1, at, rules) }); err != nil {
		t.Fatal(err)
	}
	return views(t, q, at)
}
func views(t *testing.T, q *sql.DB, at int64) []RuleView {
	t.Helper()
	if err := transaction(t, q, func(tx *sql.Tx) error { return ValidateState(context.Background(), tx) }); err != nil {
		t.Fatalf("persistent validation: %v", err)
	}
	v, err := Views(context.Background(), q, 1, at)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHundredClaimsCompeteForOneSlotAcrossAllDimensions(t *testing.T) {
	q := newQuotaDB(t)
	replace(t, q, testNow, rule("calls", "1"), rule("tokens", "5"), rule("credits", "0.005"))
	a := Amounts{Calls: mag(1), Tokens: mag(5), Credits: mag(5)}
	var group sync.WaitGroup
	results := make(chan error, 100)
	for i := 0; i < 100; i++ {
		group.Add(1)
		go func() { defer group.Done(); _, err := newClaim(t, q, testNow, a); results <- err }()
	}
	group.Wait()
	close(results)
	winners, limited := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrLimited) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || limited != 99 {
		t.Fatal(winners, limited)
	}
	for _, v := range views(t, q, testNow) {
		if v.Used != "0" || v.Remaining != "0" {
			t.Fatal(v)
		}
	}
	var receipts, held int
	if err := q.QueryRow(`SELECT (SELECT COUNT(*) FROM donation_quota_receipts),rows_held FROM donation_quota_capacity WHERE id=1`).Scan(&receipts, &held); err != nil || receipts != 3 || held != 3 {
		t.Fatal(receipts, held, err)
	}
}

func TestMarkerAndTerminalFailureRollbackAllQuotaFacts(t *testing.T) {
	q := newQuotaDB(t)
	replace(t, q, testNow, rule("calls", "3"), rule("tokens", "10"))
	a := Amounts{Calls: mag(1), Tokens: mag(5)}
	id, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, id, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Exec(`CREATE TRIGGER reject_marker BEFORE INSERT ON dispatch_response_starts BEGIN SELECT RAISE(ABORT,'marker unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	err = transaction(t, q, func(tx *sql.Tx) error {
		at, err := Start(context.Background(), tx, id, testNow+1)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO dispatch_response_starts(claim_id,started_at) VALUES(?,?)`, id, at)
		return err
	})
	if err == nil {
		t.Fatal("failed marker committed")
	}
	requireUsage(t, views(t, q, testNow+1)[0], "0", "1", "2", "waiting_first_success")
	if _, err := q.Exec(`DROP TRIGGER reject_marker`); err != nil {
		t.Fatal(err)
	}
	start(t, q, id, testNow+2)
	if _, err := q.Exec(`CREATE TRIGGER reject_terminal BEFORE UPDATE OF state ON dispatch_claims WHEN NEW.state='committed' BEGIN SELECT RAISE(ABORT,'terminal unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	err = transaction(t, q, func(tx *sql.Tx) error {
		if err := Settle(context.Background(), tx, id, testNow+3, a, true); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE dispatch_claims SET state='committed',secret_ref_id=NULL,terminal_at=?,donor_reward_state='zero',donor_reward_actual_milli=0 WHERE id=?`, testNow+3, id)
		return err
	})
	if err == nil {
		t.Fatal("failed terminal committed")
	}
	requireUsage(t, views(t, q, testNow+3)[1], "0", "5", "5", "available")
	if _, err := q.Exec(`DROP TRIGGER reject_terminal`); err != nil {
		t.Fatal(err)
	}
	terminal(t, q, id, testNow+4, a, true)
	requireUsage(t, views(t, q, testNow+4)[1], "5", "0", "5", "available")
	if err := transaction(t, q, func(tx *sql.Tx) error {
		return Settle(context.Background(), tx, id, testNow+5, Amounts{Calls: mag(1), Tokens: mag(6)}, true)
	}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestCapacityReservesFutureRowsAndReusesSameSecondBucket(t *testing.T) {
	q := newQuotaDB(t)
	r := rule("calls", "3")
	r.Mode = "sliding"
	r.Alignment = nil
	replace(t, q, testNow, r)
	a := Amounts{Calls: mag(1)}
	first, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, first, testNow); err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, second, testNow); err != nil {
		t.Fatal(err)
	}
	// Isolate the admission/capacity boundary without allocating millions of
	// unrelated rows; full-scale row-count validation is a separate gate.
	if _, err := q.Exec(`UPDATE donation_quota_capacity SET rows_used=?`, MaxRows-2); err != nil {
		t.Fatal(err)
	}
	if _, err := newClaim(t, q, testNow, a); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	start(t, q, first, testNow+1)
	start(t, q, second, testNow+1)
	terminal(t, q, second, testNow+2, a, true)
	terminal(t, q, first, testNow+3, a, true)
	var used, held int
	if err := q.QueryRow(`SELECT rows_used,rows_held FROM donation_quota_capacity WHERE id=1`).Scan(&used, &held); err != nil || used != MaxRows-1 || held != 0 {
		t.Fatal(used, held, err)
	}
	if _, err := q.Exec(`UPDATE donation_quota_capacity SET rows_used=1`); err != nil {
		t.Fatal(err)
	}
	requireUsage(t, views(t, q, testNow+3)[0], "2", "0", "1", "available")
}

func TestSlidingCalendarLeftBoundaryCanReenterRetainedFacts(t *testing.T) {
	q := newQuotaDB(t)
	instant := func(text string) int64 {
		v, err := time.Parse(time.RFC3339, text)
		if err != nil {
			t.Fatal(err)
		}
		return v.Unix()
	}
	success := instant("2026-03-08T07:30:00Z")
	r := rule("calls", "1")
	r.Mode = "sliding"
	r.Alignment = nil
	r.Interval = "day"
	r.TimeZone = "America/New_York"
	replace(t, q, success, r)
	a := Amounts{Calls: mag(1)}
	id, err := newClaim(t, q, success, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, id, success); err != nil {
		t.Fatal(err)
	}
	start(t, q, id, success)
	terminal(t, q, id, success+1, a, true)
	move := func(at int64) {
		t.Helper()
		if err := transaction(t, q, func(tx *sql.Tx) error {
			epochs, err := currentEpochs(context.Background(), tx, 1)
			if err != nil {
				return err
			}
			return advance(context.Background(), tx, &epochs[0], at)
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := instant("2026-03-09T06:59:00Z")
	after := instant("2026-03-09T07:00:00Z")
	move(before)
	requireUsage(t, views(t, q, before)[0], "0", "0", "1", "available")
	move(after)
	requireUsage(t, views(t, q, after)[0], "1", "0", "0", "limited")
	if views(t, q, after)[0].NextTransitionAt != nil {
		t.Fatal("sliding view promised an unproven transition")
	}
}

func TestStateValidationFailsClosedForCacheReceiptAndCapacityDrift(t *testing.T) {
	for _, corrupt := range []string{
		`UPDATE donation_quota_capacity SET rows_held=rows_held+1`,
		`UPDATE donation_quota_epochs SET pending_reserved=zeroblob(16)`,
		`UPDATE donation_quota_epochs SET window_used=x'00000000000000000000000000000001'`,
		`UPDATE donation_quota_epochs SET window_left=window_left-1`,
	} {
		t.Run(corrupt, func(t *testing.T) {
			q := newQuotaDB(t)
			r := rule("calls", "2")
			r.Mode = "sliding"
			r.Alignment = nil
			replace(t, q, testNow, r)
			if _, err := newClaim(t, q, testNow, Amounts{Calls: mag(1)}); err != nil {
				t.Fatal(err)
			}
			if _, err := q.Exec(corrupt); err != nil {
				t.Fatal(err)
			}
			if err := transaction(t, q, func(tx *sql.Tx) error { return ValidateState(context.Background(), tx) }); !errors.Is(err, ErrInvariant) {
				t.Fatal(err)
			}
		})
	}
}

func TestCleanupPreservesUnfinishedFactsAndHonorsOneSharedBudget(t *testing.T) {
	q := newQuotaDB(t)
	r := rule("calls", "5")
	r.Mode = "sliding"
	r.Alignment = nil
	replace(t, q, testNow, r)
	a := Amounts{Calls: mag(1)}
	first, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, first, testNow); err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, second, testNow); err != nil {
		t.Fatal(err)
	}
	start(t, q, first, testNow)
	start(t, q, second, testNow+1)
	terminal(t, q, first, testNow+2, a, true)
	later := testNow + 36*86400
	if err := transaction(t, q, func(tx *sql.Tx) error {
		n, err := Cleanup(context.Background(), tx, later, 1)
		if n != 1 {
			t.Fatalf("cleanup size %d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := q.QueryRow(`SELECT COUNT(*) FROM donation_quota_buckets`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if err := transaction(t, q, func(tx *sql.Tx) error { _, err := Cleanup(context.Background(), tx, later, CleanupBatch); return err }); err != nil {
		t.Fatal(err)
	}
	if err := q.QueryRow(`SELECT COUNT(*) FROM donation_quota_buckets`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	requireUsage(t, views(t, q, later)[0], "0", "0", "5", "available")
	terminal(t, q, second, later+1, a, true)
	if err := transaction(t, q, func(tx *sql.Tx) error { _, err := Cleanup(context.Background(), tx, later+1, CleanupBatch); return err }); err != nil {
		t.Fatal(err)
	}
	if err := q.QueryRow(`SELECT rows_used FROM donation_quota_capacity WHERE id=1`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	views(t, q, later+1)
}

func newClaim(t *testing.T, q *sql.DB, at int64, amounts Amounts) (string, error) {
	t.Helper()
	id, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		t.Fatal(err)
	}
	request, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	err = transaction(t, q, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO logical_requests(id,route_kind,state,attempt_limit,accounting_state,settlement_destination,ledger_rows_remaining,created_at) VALUES(?,'charity_chat_completions','accepted',1,'none','user',zeroblob(16),?)`, request, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,secret_ref_id,donation_key_id,claim_now,state,reserved_calls,reserved_tokens,reserved_price_milli,donor_reward_state) VALUES(?,?,1,'charity',1,1,?,'claimed',?,?,?,'pending')`, id, request, at, amounts.Calls.Big().Int64(), amounts.Tokens.Big().Int64(), amounts.Credits.Big().Int64()); err != nil {
			return err
		}
		return Reserve(context.Background(), tx, id, at)
	})
	return id, err
}

func dispatch(t *testing.T, q *sql.DB, id string, at int64) error {
	t.Helper()
	return transaction(t, q, func(tx *sql.Tx) error {
		if err := Reconcile(context.Background(), tx, id, at); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE dispatch_claims SET state='dispatched',dispatched_at=? WHERE id=?`, at, id)
		return err
	})
}

func start(t *testing.T, q *sql.DB, id string, at int64) int64 {
	t.Helper()
	var success int64
	err := transaction(t, q, func(tx *sql.Tx) error {
		var err error
		success, err = Start(context.Background(), tx, id, at)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO dispatch_response_starts(claim_id,started_at) VALUES(?,?)`, id, success)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return success
}

func terminal(t *testing.T, q *sql.DB, id string, at int64, a Amounts, success bool) {
	t.Helper()
	err := transaction(t, q, func(tx *sql.Tx) error {
		if err := Settle(context.Background(), tx, id, at, a, success); err != nil {
			return err
		}
		if err := Settle(context.Background(), tx, id, at, a, success); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE dispatch_claims SET state='committed',secret_ref_id=NULL,donor_reward_actual_milli=0,donor_reward_state='zero',terminal_at=? WHERE id=?`, at, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func requireUsage(t *testing.T, v RuleView, used, reserved, remaining, state string) {
	t.Helper()
	if v.Used != used || v.Reserved != reserved || v.Remaining != remaining || v.State != state {
		t.Fatalf("usage=%+v want %s/%s/%s/%s", v, used, reserved, remaining, state)
	}
}

func TestRuleValidationAndWideAmounts(t *testing.T) {
	maximum := "340282366920938463463374607431768211455"
	for _, v := range []RuleInput{rule("calls", maximum), rule("credits", "340282366920938463463374607431768211.455"), rule("tokens", "0")} {
		if err := Validate(v); err != nil {
			t.Fatalf("valid rule %v: %v", v, err)
		}
	}
	for _, amount := range []string{"", "01", "-1", "+1", "1e3", " 1", "1.", "1.000", "0.0001", "340282366920938463463374607431768211.456"} {
		if Validate(rule("credits", amount)) == nil {
			t.Fatalf("accepted credits %q", amount)
		}
	}
	for _, mutate := range []func(*RuleInput){
		func(r *RuleInput) { r.TimeZone = "../UTC" }, func(r *RuleInput) { r.Alignment = nil }, func(r *RuleInput) { r.Alignment = ptr("calendar") },
		func(r *RuleInput) { r.WeekStartsOn = ptr(1) }, func(r *RuleInput) { r.Mode = "sliding" }, func(r *RuleInput) { r.Limit = maximum + "0" },
	} {
		r := rule("calls", "1")
		mutate(&r)
		if Validate(r) == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
}

func TestColdValidationRejectsPeriodAndSettledCallCorruption(t *testing.T) {
	for _, mode := range []string{"first_success", "calendar"} {
		for _, corrupt := range []string{"missing current pointer", "wrong period end", "settled calls changed"} {
			t.Run(mode+"/"+corrupt, func(t *testing.T) {
				q := newQuotaDB(t)
				r := rule("calls", "10")
				if mode == "calendar" {
					r.Alignment = ptr(mode)
					r.Interval = "day"
				}
				replace(t, q, testNow, r)
				a := Amounts{Calls: mag(1)}
				id, err := newClaim(t, q, testNow, a)
				if err != nil {
					t.Fatal(err)
				}
				if err := dispatch(t, q, id, testNow); err != nil {
					t.Fatal(err)
				}
				start(t, q, id, testNow)
				terminal(t, q, id, testNow+1, a, true)
				views(t, q, testNow+1)
				switch corrupt {
				case "missing current pointer":
					_, err = q.Exec(`UPDATE donation_quota_epochs SET current_period_start=NULL`)
				case "wrong period end":
					_, err = q.Exec(`UPDATE donation_quota_periods SET end_at=end_at+1`)
				case "settled calls changed":
					err = transaction(t, q, func(tx *sql.Tx) error {
						if _, err := tx.Exec(`UPDATE donation_quota_receipts SET actual_mag=?`, db.EncodeU128(mag(2))); err != nil {
							return err
						}
						_, err := tx.Exec(`UPDATE donation_quota_periods SET used_mag=?`, db.EncodeU128(mag(2)))
						return err
					})
				}
				if err != nil {
					t.Fatal("corruption fixture", err)
				}
				err = transaction(t, q, func(tx *sql.Tx) error { return ValidateState(context.Background(), tx) })
				if !errors.Is(err, ErrInvariant) {
					t.Fatal("accepted corrupt state", err)
				}
			})
		}
	}
}

func TestPeriodCleanupPersistsObservationBeforeClockRollback(t *testing.T) {
	for _, alignment := range []string{"first_success", "calendar"} {
		t.Run(alignment, func(t *testing.T) {
			q := newQuotaDB(t)
			r := rule("calls", "1")
			if alignment == "calendar" {
				r.Alignment = ptr(alignment)
				r.Interval = "day"
			}
			replace(t, q, testNow, r)
			a := Amounts{Calls: mag(1)}
			id, err := newClaim(t, q, testNow, a)
			if err != nil {
				t.Fatal(err)
			}
			if err := dispatch(t, q, id, testNow); err != nil {
				t.Fatal(err)
			}
			start(t, q, id, testNow)
			terminal(t, q, id, testNow+1, a, true)
			v := views(t, q, testNow+1)
			collectedAt := *v[0].PeriodEnd + 10
			if err := transaction(t, q, func(tx *sql.Tx) error { _, err := Cleanup(context.Background(), tx, collectedAt, 100); return err }); err != nil {
				t.Fatal(err)
			}
			views(t, q, testNow+2)
			id, err = newClaim(t, q, testNow+2, a)
			if err != nil {
				t.Fatal(err)
			}
			if err := dispatch(t, q, id, testNow+2); err != nil {
				t.Fatal(err)
			}
			if at := start(t, q, id, testNow+2); at != collectedAt {
				t.Fatal("cleanup observation lost", at, collectedAt)
			}
		})
	}
}

func TestDatabaseBusyAtMarkerAndTerminalKeepsDurableFactsForRetry(t *testing.T) {
	q := newQuotaDB(t)
	replace(t, q, testNow, rule("calls", "3"), rule("tokens", "10"))
	a := Amounts{Calls: mag(1), Tokens: mag(5)}
	id, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, id, testNow); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := q.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := q.Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"marker", "terminal"} {
		locked, err := other.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := locked.Exec(`UPDATE site_config SET value=value WHERE key='charity_enabled'`); err != nil {
			_ = locked.Rollback()
			t.Fatal(err)
		}
		err = transaction(t, q, func(tx *sql.Tx) error {
			if stage == "marker" {
				at, err := Start(context.Background(), tx, id, testNow+1)
				if err != nil {
					return err
				}
				_, err = tx.Exec(`INSERT INTO dispatch_response_starts(claim_id,started_at) VALUES(?,?)`, id, at)
				return err
			}
			return Settle(context.Background(), tx, id, testNow+2, a, true)
		})
		if rollbackErr := locked.Rollback(); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") {
			t.Fatal("expected real SQLite lock contention", stage, err)
		}
		v := views(t, q, testNow+2)
		if stage == "marker" {
			requireUsage(t, v[0], "0", "1", "2", "waiting_first_success")
			start(t, q, id, testNow+1)
		} else {
			requireUsage(t, v[1], "0", "5", "5", "available")
			terminal(t, q, id, testNow+2, a, true)
		}
	}
	requireUsage(t, views(t, q, testNow+2)[1], "5", "0", "5", "available")
}

func TestRetirementSharesBoundedRuleBudgetAndPreservesCurrentPointers(t *testing.T) {
	q := newQuotaDB(t)
	rules := make([]RuleInput, MaxRules)
	for i := range rules {
		rules[i] = rule("calls", "1")
	}
	replace(t, q, testNow, rules...)
	for remaining := MaxRules; remaining > 0; {
		err := transaction(t, q, func(tx *sql.Tx) error {
			ready, retired, err := RetireDonation(context.Background(), tx, 1, testNow, 3)
			if err != nil {
				return err
			}
			if ready || retired != min(3, remaining) {
				t.Fatal(ready, retired, remaining)
			}
			remaining -= retired
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := views(t, q, testNow); len(got) != remaining {
			t.Fatal(len(got), remaining)
		}
	}
}

func TestRuleReplacementPreservesLimitChangesAndRetiresStructuralEpochs(t *testing.T) {
	q := newQuotaDB(t)
	v := replace(t, q, testNow, rule("calls", "10"), rule("tokens", "20"))
	a, b := v[0].RuleInput, v[1].RuleInput
	a.Limit = "0"
	v = replace(t, q, testNow, b, a)
	if *v[0].ID != *b.ID || *v[1].ID != *a.ID {
		t.Fatal("order did not change")
	}
	var epochNumber int64
	if err := q.QueryRow(`SELECT current_epoch FROM donation_quota_rules WHERE id=?`, *a.ID).Scan(&epochNumber); err != nil || epochNumber != 1 {
		t.Fatal(epochNumber, err)
	}
	a.Interval = "day"
	v = replace(t, q, testNow, a)
	if err := q.QueryRow(`SELECT current_epoch FROM donation_quota_rules WHERE id=?`, *a.ID).Scan(&epochNumber); err != nil || epochNumber != 2 {
		t.Fatal(epochNumber, err)
	}
	var retired int
	if err := q.QueryRow(`SELECT COUNT(*) FROM donation_quota_epochs WHERE retired_at=?`, testNow).Scan(&retired); err != nil || retired != 2 {
		t.Fatal(retired, err)
	}
	before := *v[0].ID
	if err := transaction(t, q, func(tx *sql.Tx) error { return Replace(context.Background(), tx, 1, testNow, []RuleInput{a, a}) }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if got := views(t, q, testNow); len(got) != 1 || *got[0].ID != before {
		t.Fatal("invalid full replacement changed set")
	}
	if got := replace(t, q, testNow); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestFirstSuccessReservationAccountingAndExpiredIdlePeriod(t *testing.T) {
	for interval, seconds := range map[string]int64{"1h": 3600, "5h": 18000} {
		t.Run(interval, func(t *testing.T) { testFirstSuccessPeriod(t, interval, seconds) })
	}
}

func testFirstSuccessPeriod(t *testing.T, interval string, seconds int64) {
	t.Helper()
	q := newQuotaDB(t)
	rules := []RuleInput{rule("calls", "2"), rule("tokens", "10"), rule("credits", "0.01")}
	for i := range rules {
		rules[i].Interval = interval
	}
	replace(t, q, testNow, rules...)
	a := Amounts{Calls: mag(1), Tokens: mag(5), Credits: mag(5)}
	id, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	v := views(t, q, testNow)
	requireUsage(t, v[0], "0", "1", "1", "waiting_first_success")
	if err := dispatch(t, q, id, testNow); err != nil {
		t.Fatal(err)
	}
	success := start(t, q, id, testNow+1)
	v = views(t, q, testNow+1)
	requireUsage(t, v[0], "1", "0", "1", "available")
	requireUsage(t, v[1], "0", "5", "5", "available")
	if v[0].PeriodStart == nil || *v[0].PeriodStart != success || *v[0].PeriodEnd != success+seconds {
		t.Fatal(v[0])
	}
	terminal(t, q, id, testNow+2, Amounts{Calls: mag(1), Tokens: mag(15), Credits: mag(20)}, true)
	v = views(t, q, testNow+2)
	requireUsage(t, v[1], "15", "0", "0", "limited")
	requireUsage(t, v[2], "0.02", "0", "0", "limited")
	v = views(t, q, success+seconds-1)
	requireUsage(t, v[1], "15", "0", "0", "limited")
	v = views(t, q, success+seconds)
	requireUsage(t, v[0], "0", "0", "2", "waiting_first_success")
	next := testNow + seconds + 6000
	id, err = newClaim(t, q, next, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, id, next); err != nil {
		t.Fatal(err)
	}
	start(t, q, id, next)
	if v := views(t, q, next); *v[0].PeriodEnd != next+seconds {
		t.Fatal(v)
	}
}

func TestUnsuccessfulAttemptDoesNotStartPeriodAndMultiRuleFailureRollsBack(t *testing.T) {
	q := newQuotaDB(t)
	replace(t, q, testNow, rule("calls", "2"), rule("tokens", "4"))
	a := Amounts{Calls: mag(1), Tokens: mag(5)}
	if _, err := newClaim(t, q, testNow, a); !errors.Is(err, ErrLimited) {
		t.Fatal(err)
	}
	var count, held int
	if err := q.QueryRow(`SELECT (SELECT COUNT(*) FROM dispatch_claims),rows_held FROM donation_quota_capacity WHERE id=1`).Scan(&count, &held); err != nil || count != 0 || held != 0 {
		t.Fatal(count, held, err)
	}
	a.Tokens = mag(4)
	id, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, id, testNow); err != nil {
		t.Fatal(err)
	}
	terminal(t, q, id, testNow+1, Amounts{}, false)
	for _, v := range views(t, q, testNow+1) {
		if v.Used != "0" || v.Reserved != "0" || v.State != "waiting_first_success" || v.PeriodStart != nil {
			t.Fatal(v)
		}
	}
}

func TestDispatchRechecksPreviouslyEmptySetAndSameSecondChanges(t *testing.T) {
	q := newQuotaDB(t)
	a := Amounts{Calls: mag(1)}
	id, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	v := replace(t, q, testNow, rule("calls", "0"))
	if err := dispatch(t, q, id, testNow); !errors.Is(err, ErrLimited) {
		t.Fatal(err)
	}
	r := v[0].RuleInput
	r.Limit = "1"
	replace(t, q, testNow, r)
	if err := dispatch(t, q, id, testNow); err != nil {
		t.Fatal(err)
	}
	r.Interval = "day"
	replace(t, q, testNow, r)
	start(t, q, id, testNow)
	terminal(t, q, id, testNow+1, a, true)
	v = views(t, q, testNow+1)
	requireUsage(t, v[0], "0", "0", "1", "waiting_first_success")
	var oldUsed []byte
	if err := q.QueryRow(`SELECT used_mag FROM donation_quota_periods WHERE rule_id=? AND epoch=1`, *r.ID).Scan(&oldUsed); err != nil {
		t.Fatal(err)
	}
	n, _ := magnitude(oldUsed)
	if n != mag(1) {
		t.Fatal(n)
	}
}

func TestNaturalMidnightKeepsUnstartedReservationsAndOriginalSuccessPeriod(t *testing.T) {
	q := newQuotaDB(t)
	midnight := (testNow/86400 + 1) * 86400
	r := rule("calls", "2")
	r.Interval = "day"
	r.Alignment = ptr("calendar")
	replace(t, q, midnight-60, r)
	a := Amounts{Calls: mag(1)}
	first, err := newClaim(t, q, midnight-30, a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newClaim(t, q, midnight-20, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, first, midnight-30); err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, second, midnight-20); err != nil {
		t.Fatal(err)
	}
	start(t, q, first, midnight-1)
	v := views(t, q, midnight)
	requireUsage(t, v[0], "0", "1", "1", "available")
	start(t, q, second, midnight+1)
	terminal(t, q, second, midnight+2, a, true)
	terminal(t, q, first, midnight+3, a, true)
	v = views(t, q, midnight+3)
	requireUsage(t, v[0], "1", "0", "1", "available")
	if *v[0].PeriodStart != midnight {
		t.Fatal(v)
	}
}

func TestSlidingExactLeftBoundaryAndClockRollback(t *testing.T) {
	for interval, seconds := range map[string]int64{"1h": 3600, "5h": 18000} {
		t.Run(interval, func(t *testing.T) { testSlidingHourlyBoundary(t, interval, seconds) })
	}
}

func testSlidingHourlyBoundary(t *testing.T, interval string, seconds int64) {
	t.Helper()
	q := newQuotaDB(t)
	r := rule("calls", "1")
	r.Interval = interval
	r.Mode = "sliding"
	r.Alignment = nil
	replace(t, q, testNow, r)
	a := Amounts{Calls: mag(1)}
	id, err := newClaim(t, q, testNow, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, id, testNow); err != nil {
		t.Fatal(err)
	}
	start(t, q, id, testNow)
	terminal(t, q, id, testNow+1, a, true)
	requireUsage(t, views(t, q, testNow+seconds-1)[0], "1", "0", "0", "limited")
	requireUsage(t, views(t, q, testNow+seconds)[0], "0", "0", "1", "available")
	id, err = newClaim(t, q, testNow+seconds, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch(t, q, id, testNow+seconds); err != nil {
		t.Fatal(err)
	}
	if at := start(t, q, id, testNow+10); at != testNow+seconds {
		t.Fatal("clock rollback escaped clamp", at)
	}
	requireUsage(t, views(t, q, testNow+10)[0], "1", "0", "0", "limited")
}
