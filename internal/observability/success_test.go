package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestRecentSuccessBatchWindowAndSamples(t *testing.T) {
	r, userID := newObservedDatabase(t)
	now := r.now().Unix()
	capture := now - 10
	if _, err := r.db.Exec(`UPDATE observability_state SET capture_started_at=?,last_access_gap_at=NULL WHERE id=1`, capture); err != nil {
		t.Fatal(err)
	}
	models := make([]int64, 4)
	for index := range models {
		models[index] = successModel(t, r.db, index)
	}
	for _, sample := range []struct {
		at         int64
		result     string
		dispatched bool
	}{
		{now - 86400, "success", true}, {now - 1, "failure", true}, {now - 1, "cancelled", true},
		{now - 1, "failure", false}, {now - 1, "cancelled", false},
		{now, "success", true}, {now + 1, "failure", true}, {now - 86401, "success", true},
	} {
		successOutcome(t, r.db, userID, models[0], sample.at, sample.result, sample.dispatched)
	}
	for index := range 20 {
		result := "success"
		if index >= 12 {
			result = "failure"
		}
		successOutcome(t, r.db, userID, models[1], now-1, result, true)
	}
	successOutcome(t, r.db, userID, models[2], now-1, "cancelled", true)
	tx, err := r.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ids := []int64{models[1], models[3], models[0], models[2], models[0], math.MaxInt64}
	got, err := RecentSuccessBatchTx(context.Background(), tx, ids, now)
	if err != nil {
		t.Fatal(err)
	}
	base := SuccessRate{WindowStart: now - 86400, AsOf: now, CaptureStartedAt: capture, InsufficientSample: true}
	half, sixtyPercent := 0.5, 0.6
	mixed, sufficient, cancelled := base, base, base
	mixed.Success, mixed.Failure, mixed.Cancelled, mixed.SampleCount, mixed.Rate = 1, 1, 1, 2, &half
	sufficient.Success, sufficient.Failure, sufficient.SampleCount, sufficient.Rate, sufficient.InsufficientSample = 12, 8, 20, &sixtyPercent, false
	cancelled.Cancelled = 1
	want := map[int64]SuccessRate{models[0]: mixed, models[1]: sufficient, models[2]: cancelled, models[3]: base, math.MaxInt64: base}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batch: got %+v, want %+v", got, want)
	}
	for id, expected := range want {
		single, err := RecentSuccessTx(context.Background(), tx, id, now)
		if err != nil || !reflect.DeepEqual(single, expected) {
			t.Fatalf("single model %d: got %+v err=%v, want %+v", id, single, err, expected)
		}
	}
}

func TestRecentSuccessBatchClampsWindowAndAcceptsMaximumPage(t *testing.T) {
	r, userID := newObservedDatabase(t)
	modelID := successModel(t, r.db, 0)
	for _, at := range []int64{0, 9, 10} {
		successOutcome(t, r.db, userID, modelID, at, "success", true)
	}
	ids := make([]int64, 100)
	for index := range ids {
		ids[index] = int64(index + 1)
	}
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := RecentSuccessBatchTx(context.Background(), tx, ids, 10)
	if err != nil || len(got) != 100 {
		t.Fatalf("maximum page: count=%d err=%v", len(got), err)
	}
	if got[modelID].WindowStart != 0 || got[modelID].AsOf != 10 || got[modelID].Success != 2 || got[modelID].SampleCount != 2 ||
		got[modelID].Rate == nil || *got[modelID].Rate != 1 || !got[modelID].InsufficientSample {
		t.Fatalf("clamped window: %+v", got[modelID])
	}
	if zero := got[100]; zero.Success != 0 || zero.SampleCount != 0 || zero.Rate != nil || !zero.InsufficientSample ||
		zero.CaptureStartedAt != got[modelID].CaptureStartedAt || zero.AsOf != 10 || zero.WindowStart != 0 {
		t.Fatalf("zero sample: %+v", zero)
	}
}

func TestRecentSuccessBatchEmptyAndReadErrors(t *testing.T) {
	r, _ := newObservedDatabase(t)
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, test := range []struct {
		ctx context.Context
		tx  *sql.Tx
		at  int64
	}{{nil, tx, 10}, {context.Background(), nil, 10}, {context.Background(), tx, -1}} {
		if _, err := RecentSuccessBatchTx(test.ctx, test.tx, []int64{1}, test.at); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid call: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RecentSuccessBatchTx(ctx, tx, []int64{1}, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	if _, err := tx.Exec(`DELETE FROM observability_state WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	empty, err := RecentSuccessBatchTx(context.Background(), tx, nil, 10)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty batch: %+v %v", empty, err)
	}
	if got, err := RecentSuccessBatchTx(context.Background(), tx, []int64{1}, 10); !errors.Is(err, sql.ErrNoRows) || got != nil {
		t.Fatalf("missing capture state: %+v %v", got, err)
	}
}

func successModel(t *testing.T, database *sql.DB, index int) int64 {
	t.Helper()
	name := fmt.Sprintf("model-%d", index)
	result, err := database.Exec(`INSERT INTO charity_models(provider,model,full_name,pricing_mode,created_at,updated_at) VALUES('test',?,?,'per_request',0,0)`, name, "[公益]test/"+name)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func successOutcome(t *testing.T, database *sql.DB, userID, modelID, at int64, result string, dispatched bool) {
	t.Helper()
	_, logID := observedLog(t, &Repository{db: database}, userID, at)
	if _, err := database.Exec(`INSERT INTO charity_request_outcomes(request_log_id,model_id,completed_at,dispatched,result) VALUES(?,?,?,?,?)`, logID, modelID, at, dispatched, result); err != nil {
		t.Fatal(err)
	}
}

func TestRecentSuccessBatchUsesCallerSnapshot(t *testing.T) {
	r, userID := newObservedDatabase(t)
	now := r.now().Unix()
	modelID := successModel(t, r.db, 0)
	oldCapture, newCapture := now-10, now-5
	if _, err := r.db.Exec(`UPDATE observability_state SET capture_started_at=?,last_access_gap_at=NULL WHERE id=1`, oldCapture); err != nil {
		t.Fatal(err)
	}
	successOutcome(t, r.db, userID, modelID, now-1, "success", true)
	var sequence int
	var name, path string
	if err := r.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := r.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var capture int64
	if err := tx.QueryRow(`SELECT capture_started_at FROM observability_state WHERE id=1`).Scan(&capture); err != nil || capture != oldCapture {
		t.Fatalf("establish snapshot: capture=%d err=%v", capture, err)
	}
	if _, err := writer.Exec(`UPDATE observability_state SET capture_started_at=? WHERE id=1`, newCapture); err != nil {
		t.Fatal(err)
	}
	successOutcome(t, writer, userID, modelID, now-1, "failure", true)
	got, err := RecentSuccessBatchTx(context.Background(), tx, []int64{modelID}, now)
	if err != nil || got[modelID].CaptureStartedAt != oldCapture || got[modelID].Success != 1 || got[modelID].Failure != 0 {
		t.Fatalf("caller snapshot: %+v %v", got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	currentTx, err := r.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer currentTx.Rollback()
	current, err := RecentSuccessTx(t.Context(), currentTx, modelID, now)
	if err != nil || current.CaptureStartedAt != newCapture || current.Success != 1 || current.Failure != 1 {
		t.Fatalf("new snapshot: %+v %v", current, err)
	}
}
