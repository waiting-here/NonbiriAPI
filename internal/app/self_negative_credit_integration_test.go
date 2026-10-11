package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestSelfHTTPNegativeCredit(t *testing.T) {
	var dispatched atomic.Int64
	f := newModelHTTPFixture(t, nil, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
		dispatched.Add(1)
		var body struct {
			Scenario string `json:"scenario"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if body.Scenario == "error" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"Capacity exhausted","code":"busy"}}`)
			return
		}
		switch r.URL.Path {
		case "/v1/chat/completions":
			_, _ = io.WriteString(w, `{"id":"chat-result","object":"chat.completion","created":1,"model":"private-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
		case "/v1/embeddings":
			_, _ = io.WriteString(w, `{"object":"list","model":"private-model","data":[{"object":"embedding","index":0,"embedding":[0.25,-0.5]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`)
		case "/v1/images/generations":
			_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"aGVsbG8="}],"usage":{"input_tokens":7,"output_tokens":11,"total_tokens":18}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	f.exec(t, `UPDATE models SET model_types=7`)
	f.exec(t, `UPDATE site_config SET value='0' WHERE key='charity_min_chars'`)

	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	account, err := ledger.UserAccount(context.Background(), tx, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAccount(context.Background(), tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	operationID, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewAdminUserAdjustment(
		ledger.Meta{OperationID: operationID, ActorUserID: f.userID, CreatedAt: time.Now().Unix()},
		account.ID, external.ID, ledger.AmountFromMilli(-1_000_750), 0, ledger.Amount{}, "negative credit fixture",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	for _, item := range []struct{ name, path, input string }{
		{"chat", "/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}],"stream":false`},
		{"embedding", "/v1/embeddings", `"input":"hello","dimensions":2`},
		{"image", "/v1/images/generations", `"prompt":"Draw a lake","n":1`},
	} {
		for _, scenario := range []string{"success", "error"} {
			t.Run(item.name+"/"+scenario, func(t *testing.T) {
				status, body := f.post(t, item.path, `{"model":"provider/self",`+item.input+`,"scenario":"`+scenario+`"}`)
				wantStatus := http.StatusOK
				wantResult := "success"
				if scenario == "error" {
					wantStatus, wantResult = http.StatusTooManyRequests, "failed"
				}
				if status != wantStatus {
					t.Fatalf("status=%d want=%d body=%s", status, wantStatus, body)
				}
				var state, accounting, result string
				var reserved int64
				var remaining []byte
				if err := f.store.DB().QueryRow(`SELECT r.state,r.accounting_state,r.account_reserved_milli,r.ledger_rows_remaining,l.caller_result_class
FROM request_logs l JOIN logical_requests r ON r.id=l.logical_request_id ORDER BY l.id DESC LIMIT 1`).Scan(&state, &accounting, &reserved, &remaining, &result); err != nil {
					t.Fatal(err)
				}
				rows, err := db.DecodeU128(remaining)
				if err != nil || state != "terminal" || accounting != "committed" || reserved != 0 || rows.Big().Sign() != 0 || result != wantResult {
					t.Fatalf("terminal state=%s accounting=%s reserved=%d rows=%x result=%s error=%v", state, accounting, reserved, remaining, result, err)
				}
				checkSelfHTTPNegativeBalance(t, f)
			})
		}
	}
	if dispatched.Load() != 6 {
		t.Fatalf("self upstream dispatches=%d, want 6", dispatched.Load())
	}
	status, body := f.post(t, "/v1/embeddings", `{"model":"[公益]provider/per_request","input":"hello"}`)
	if status != http.StatusForbidden || !strings.Contains(string(body), "insufficient_credits") || dispatched.Load() != 6 {
		t.Fatalf("charity status=%d body=%s upstream dispatches=%d", status, body, dispatched.Load())
	}
	checkSelfHTTPNegativeBalance(t, f)
}

func checkSelfHTTPNegativeBalance(t *testing.T, f *embeddingHTTPFixture) {
	t.Helper()
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	account, err := ledger.UserAccount(context.Background(), tx, f.userID)
	if err != nil || account.Balance.Decimal() != "-750" {
		t.Fatalf("self balance=%s error=%v, want -750", account.Balance.Decimal(), err)
	}
	capacity, err := ledger.ReadCapacity(context.Background(), tx)
	if err != nil || capacity.ReservedFutureRows.Big().Sign() != 0 {
		t.Fatalf("remaining accounting capacity=%s error=%v", capacity.ReservedFutureRows.Decimal(), err)
	}
	var nonzeroEntries, unfinished int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM credit_entries e JOIN credit_operations o ON o.id=e.operation_id
WHERE o.kind IN ('forward_reserve','forward_settle','forward_release') AND e.delta_sign<>0`).Scan(&nonzeroEntries); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM dispatch_claims WHERE state IN ('claimed','dispatched')`).Scan(&unfinished); err != nil {
		t.Fatal(err)
	}
	if nonzeroEntries != 0 || unfinished != 0 {
		t.Fatalf("self nonzero ledger entries=%d unfinished claims=%d", nonzeroEntries, unfinished)
	}
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
}
