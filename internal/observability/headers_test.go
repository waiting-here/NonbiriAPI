package observability

import (
	"context"
	"encoding/base64"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
)

func TestHeaderFactsSameTransactionBudgetCascadeAndLateCallback(t *testing.T) {
	r, user := newObservedDatabase(t)
	if _, err := r.db.Exec("UPDATE site_config SET value='1' WHERE key='request_header_budget_mib'"); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 400000)
	random := rand.New(rand.NewPCG(1, 2))
	for i := range payload {
		payload[i] = byte(random.Uint32())
	}
	request := httptest.NewRequest("POST", "http://example.invalid", nil)
	request.Header.Set("X-Full", base64.StdEncoding.EncodeToString(payload))
	request.Header.Set("Authorization", "secret-never-persisted")
	ctx := headerfacts.WithRequest(context.Background(), request)
	ids := make([]string, 4)
	for i := range ids {
		ids[i], _ = observedLog(t, r, user, r.now().Unix())
	}
	var wait sync.WaitGroup
	for _, id := range ids {
		wait.Add(1)
		go func() {
			defer wait.Done()
			tx, err := r.db.BeginTx(ctx, nil)
			if err == nil {
				err = RecordRequestHeadersTx(ctx, tx, id)
			}
			if err == nil {
				err = tx.Commit()
			} else if tx != nil {
				tx.Rollback()
			}
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	saved := scalar(t, r.db, "SELECT count(*) FROM request_header_facts WHERE save_state='saved'")
	omitted := scalar(t, r.db, "SELECT count(*) FROM request_header_facts WHERE save_state='capacity_exhausted'")
	used := scalar(t, r.db, "SELECT header_bytes FROM observability_state WHERE id=1")
	if saved != 2 || omitted != 2 || used <= 0 || used > 1<<20 || used != scalar(t, r.db, "SELECT SUM(bytes_saved) FROM request_header_facts") {
		t.Fatal(saved, omitted, used)
	}
	tx, _ := r.db.Begin()
	if err := RecordRequestHeadersTx(ctx, tx, ids[0]); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if used != scalar(t, r.db, "SELECT header_bytes FROM observability_state WHERE id=1") {
		t.Fatal("replay recounted")
	}
	// A rollback must undo both the fact and counter, without a side transaction.
	emptyID, _ := observedLog(t, r, user, r.now().Unix())
	empty := headerfacts.WithRequest(context.Background(), httptest.NewRequest("GET", "http://empty.invalid", nil))
	tx, _ = r.db.Begin()
	if err := RecordRequestHeadersTx(empty, tx, emptyID); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if used != scalar(t, r.db, "SELECT header_bytes FROM observability_state WHERE id=1") {
		t.Fatal("rollback leaked count")
	}
	if _, err := r.db.Exec("DELETE FROM request_logs WHERE logical_request_id IN (?,?,?,?)", ids[0], ids[1], ids[2], ids[3]); err != nil {
		t.Fatal(err)
	}
	if scalar(t, r.db, "SELECT header_bytes FROM observability_state WHERE id=1") != 0 {
		t.Fatal("cascade did not reclaim")
	}
	tx, _ = r.db.Begin()
	if err := RecordRequestHeadersTx(ctx, tx, ids[0]); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if scalar(t, r.db, "SELECT count(*) FROM request_header_facts") != 0 {
		t.Fatal("late callback recreated deleted root")
	}
	tx, _ = r.db.Begin()
	if err := RecordRequestHeadersTx(ctx, tx, emptyID); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if scalar(t, r.db, "SELECT count(*) FROM request_header_facts WHERE save_state='saved'") != 1 {
		t.Fatal("reclaimed capacity not reusable")
	}
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE logical_requests SET user_id=NULL WHERE user_id=?", user); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec("DELETE FROM users WHERE id=?", user); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if scalar(t, r.db, "SELECT count(*) FROM request_logs WHERE logical_request_id=? AND user_id IS NULL", emptyID) != 1 || scalar(t, r.db, "SELECT count(*) FROM request_header_facts WHERE save_state='saved'") != 1 {
		t.Fatal("account anonymization changed retained root facts")
	}

}

func TestHeaderFactsResponseCollectionAndMissingHistory(t *testing.T) {
	r, user := newObservedDatabase(t)
	id, root := observedLog(t, r, user, r.now().Unix())
	ctx := r.DiscoveryScope(context.Background(), id, 1)
	for _, status := range []int{200, 503} {
		headerfacts.CaptureResponse(ctx, &http.Response{StatusCode: status, Header: http.Header{"X-Page": {http.StatusText(status)}}})
	}
	prepared := headerfacts.PrepareResponse(ctx, id, 1)
	tx, _ := r.db.Begin()
	if err := RecordResponseHeadersTx(prepared, tx, id, 1); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	var state string
	var encoded []byte
	if err := r.db.QueryRow("SELECT save_state,encoded FROM request_header_facts WHERE request_log_id=? AND attempt_seq=1", root).Scan(&state, &encoded); err != nil {
		t.Fatal(err)
	}
	view, err := headerfacts.DecodeCollection(state, encoded, false)
	if err != nil || len(view.Responses) != 2 || *view.Responses[0].HTTPStatus != 200 || *view.Responses[1].HTTPStatus != 503 {
		t.Fatal(view, err)
	}
	tx, _ = r.db.Begin()
	missing, err := HeadersTx(context.Background(), tx, root, 0)
	tx.Commit()
	decoded, decodeErr := missing.Decode(false)
	if err != nil || decodeErr != nil || decoded.(headerfacts.View).SaveState != "not_recorded" {
		t.Fatal(decoded, err, decodeErr)
	}
	// Oversized input is an explicit omission and cannot fail business writes.
	request := httptest.NewRequest("GET", "http://example.invalid", nil)
	request.Header.Set("X-Oversized", strings.Repeat("x", headerfacts.MaxHeaderBytes))
	tx, _ = r.db.Begin()
	if err := RecordRequestHeadersTx(headerfacts.WithRequest(context.Background(), request), tx, id); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if err := r.db.QueryRow("SELECT save_state FROM request_header_facts WHERE request_log_id=? AND attempt_seq=0", root).Scan(&state); err != nil || state != "too_large" {
		t.Fatal(state, err)
	}
}

func TestRequestHeaderCaptureRetainsSourceOwnerGuard(t *testing.T) {
	r, user := newObservedDatabase(t)
	id, _ := observedLog(t, r, user, r.now().Unix())
	request := httptest.NewRequest("GET", "http://example.invalid", nil)
	request.Header.Set("X-Private", "owner-only")
	ctx := WithSource(headerfacts.WithRequest(context.Background(), request), Source{EffectiveIP: "192.0.2.10", IPQuality: "direct_peer"})
	for _, owner := range []int64{user + 1, user} {
		tx, err := r.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err = r.RecordSourceTx(ctx, tx, id, owner, "self", r.now().Unix()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		want := int64(0)
		if owner == user {
			want = 1
		}
		if scalar(t, r.db, "SELECT count(*) FROM request_header_facts") != want {
			t.Fatal("header source crossed owner boundary")
		}
	}
}
