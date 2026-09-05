package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestUserHistoryFiltersSnapshotPagesAndOwnerIsolation(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	owner, wallet := seedLedgerUser(t, tx, "history-owner")
	foreign, foreignWallet := seedLedgerUser(t, tx, "history-foreign")
	emptyUser, _ := seedLedgerUser(t, tx, "history-empty")
	external, err := CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 55)
	for i := range ids {
		ids[i] = mustLedgerID(t, "op_")
		meta := Meta{OperationID: ids[i], ActorUserID: owner, CreatedAt: ledgerTestNow + int64(i)}
		value := AmountFromMilli(7)
		if i == 0 {
			value = AmountFromMilli(9_000_000_000_000_000)
		}
		var plan Plan
		if i%2 == 0 {
			plan, err = NewAdminUserAdjustment(meta, wallet.ID, external.ID, value, 0, Amount{}, "private administrator reason")
		} else {
			plan, err = NewAntiAbusePenalty(meta, wallet.ID, external.ID, value, "private penalty reason")
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Apply(ctx, tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	foreignID := mustLedgerID(t, "op_")
	plan, err := NewAdminUserAdjustment(Meta{OperationID: foreignID, ActorUserID: foreign, CreatedAt: ledgerTestNow + 55}, foreignWallet.ID, external.ID, AmountFromMilli(1), 0, Amount{}, "foreign reason")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	read := func(user int64, filter HistoryFilter) (HistoryPage, error) {
		t.Helper()
		tx := beginLedgerTestTx(t, store.DB())
		defer tx.Rollback()
		return UserHistory(ctx, tx, user, ledgerTestNow+100, filter)
	}
	f := HistoryFilter{Page: 1, PageSize: 20}
	first, err := read(owner, f)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != "55" || first.TotalPages != "3" || len(first.Data) != 20 || first.Anchor == nil || *first.Anchor != ids[54] || first.CurrentBalance != "9000000000000" {
		t.Fatalf("first page: %+v", first)
	}
	for i, entry := range first.Data {
		if entry.OperationID != ids[54-i] || entry.RequestID != nil {
			t.Fatalf("wrong owner/order: %+v", entry)
		}
	}
	body, _ := json.Marshal(first)
	for _, forbidden := range []string{foreignID, "private", "source_id", "actor_user_id", "balance_after", "ledger_seq"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("history leaked %s", forbidden)
		}
	}
	tx = beginLedgerTestTx(t, store.DB())
	newID := mustLedgerID(t, "op_")
	plan, err = NewAdminUserAdjustment(Meta{OperationID: newID, ActorUserID: owner, CreatedAt: ledgerTestNow + 99}, wallet.ID, external.ID, AmountFromMilli(7), 0, Amount{}, "new entry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.Anchor = *first.Anchor
	f.Page = 2
	second, err := read(owner, f)
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != "55" || second.Data[0].OperationID != ids[34] || second.CurrentBalance != "9000000000000.007" {
		t.Fatalf("shifted anchor/precision: %+v", second)
	}
	f.Page = math.MaxInt64
	last, err := read(owner, f)
	if err != nil {
		t.Fatal(err)
	}
	if last.Page != "3" || len(last.Data) != 15 || last.Data[14].OperationID != ids[0] {
		t.Fatalf("last page: %+v", last)
	}
	f.PageSize = 50
	last, err = read(owner, f)
	if err != nil {
		t.Fatal(err)
	}
	if last.Page != "2" || len(last.Data) != 5 {
		t.Fatalf("page size: %+v", last)
	}
	from, to := ledgerTestNow+1, ledgerTestNow+7
	f = HistoryFilter{Page: 1, PageSize: 100, From: &from, To: &to, Direction: "expense", Category: "penalty"}
	filtered, err := read(owner, f)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != "3" || filtered.Data[0].Delta != "-0.007" {
		t.Fatalf("filter intersection: %+v", filtered)
	}
	for _, user := range []int64{foreign, emptyUser} {
		f = HistoryFilter{Page: 1, PageSize: 20, Anchor: *first.Anchor}
		if _, err := read(user, f); !errors.Is(err, ErrInvalidHistory) {
			t.Fatalf("foreign anchor: %v", err)
		}
	}
	empty, err := read(emptyUser, HistoryFilter{Page: 1, PageSize: 20})
	if err != nil || empty.Total != "0" || empty.Anchor != nil || len(empty.Data) != 0 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	if _, err := read(math.MaxInt64, HistoryFilter{Page: 1, PageSize: 20}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account: %v", err)
	}
}

func TestHistoryRequestLinksRequireCurrentCallerOwnershipAndRetention(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	owner, wallet := seedLedgerUser(t, tx, "link-owner")
	foreign, _ := seedLedgerUser(t, tx, "link-foreign")
	external, err := CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	charity, err := CodedAccount(ctx, tx, "charity_reserve")
	if err != nil {
		t.Fatal(err)
	}
	forward, err := CodedAccount(ctx, tx, "forward_reserve")
	if err != nil {
		t.Fatal(err)
	}
	apply := func(plan Plan, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(ctx, tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	meta := func() Meta {
		return Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: owner, CreatedAt: ledgerTestNow}
	}
	apply(NewCheckinAward(meta(), wallet.ID, external.ID, AmountFromMilli(10000)))
	want := map[string]*string{}
	for _, tc := range []struct {
		name               string
		logUser, completed int64
		self, link         bool
	}{
		{"own charity", owner, ledgerTestNow, false, true},
		{"foreign charity", foreign, ledgerTestNow, false, false},
		{"expired boundary", owner, ledgerTestNow - 30*86400, false, false},
		{"retained edge", owner, ledgerTestNow - 30*86400 + 1, false, true},
		{"charity in progress", owner, 0, false, false},
		{"self in progress", owner, 0, true, true},
		{"deleted owner", 0, ledgerTestNow, false, false},
	} {
		requestID := mustLedgerID(t, "req_")
		operation := meta()
		route := "charity_chat_completions"
		if tc.self {
			route = "openai_chat_completions"
			apply(NewForwardReserve(operation, requestID, wallet.ID, forward.ID, AmountFromMilli(10)))
		} else {
			apply(NewCharityReserve(operation, requestID, wallet.ID, charity.ID, AmountFromMilli(10)))
		}
		var completed, class, status, user any
		started := ledgerTestNow
		statusCode := 0
		if tc.completed != 0 {
			completed = tc.completed
			started = tc.completed
			class = "success"
			status = 200
			statusCode = 200
		}
		if tc.logUser != 0 {
			user = tc.logUser
		}
		state := "accepted"
		if tc.completed != 0 {
			state = "terminal"
		}
		if _, err := tx.Exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at,terminal_at,caller_result_class,caller_status)
VALUES(?,?,?,'test model',?,1,'none',0,'user',?,?,?, ?,?)`, requestID, user, route, state, db.EncodeU128(db.U128{}), started, completed, class, status); err != nil {
			t.Fatalf("%s request: %v", tc.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO request_logs(logical_request_id,user_id,model,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code)
VALUES(?,?,'test model',?,?,?,?,?,?)`, requestID, user, route, started, completed, class, status, statusCode); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		want[operation.OperationID] = nil
		if tc.link {
			want[operation.OperationID] = &requestID
		}
	}
	requestID := mustLedgerID(t, "req_")
	claimID := mustLedgerID(t, "clm_")
	ref, _ := LogicalRequestReservation(requestID)
	one := mustU128(t, "1")
	if err := Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,'charity_chat_completions','model','accepted',1,'reserved',0,'user',?,?)`, requestID, foreign, db.EncodeU128(one), ledgerTestNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	donorMeta := meta()
	donorPlan, err := NewDonorReward(donorMeta, requestID, claimID, external.ID, wallet.ID, owner, AmountFromMilli(3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConsumeReserved(ctx, tx, ref, donorPlan, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE logical_requests SET state='terminal',caller_result_class='success',caller_status=200,accounting_state='committed',ledger_rows_remaining=?,terminal_at=? WHERE id=?`, db.EncodeU128(db.U128{}), ledgerTestNow, requestID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want[donorMeta.OperationID] = nil
	page, err := UserHistory(ctx, tx, owner, ledgerTestNow, HistoryFilter{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Data {
		if expected, ok := want[entry.OperationID]; ok {
			if (expected == nil) != (entry.RequestID == nil) || (expected != nil && *expected != *entry.RequestID) {
				t.Fatalf("link mismatch %+v want %v", entry, expected)
			}
			delete(want, entry.OperationID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing history entries: %d", len(want))
	}
	body, _ := json.Marshal(page)
	if strings.Contains(string(body), requestID) || strings.Contains(string(body), claimID) {
		t.Fatal("donor history disclosed another caller request/claim")
	}
}
