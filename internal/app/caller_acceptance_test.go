package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type beforeBodyRead struct {
	io.Reader
	before func()
}

func (r *beforeBodyRead) Read(p []byte) (int, error) {
	if r.before != nil {
		before := r.before
		r.before = nil
		before()
	}
	return r.Reader.Read(p)
}

func TestCallerRotationDuringBodyReadPreventsEconomicAcceptance(t *testing.T) {
	for _, operation := range []string{"revoke", "rotate"} {
		t.Run(operation, func(t *testing.T) {
			f := newEmbeddingHTTPFixture(t)
			changed := false
			body := &beforeBodyRead{Reader: strings.NewReader(`{"model":"[公益]provider/per_token","input":"hello"}`), before: func() {
				statement := `UPDATE caller_keys SET generation=generation+1,key_hash=randomblob(32) WHERE user_id=?`
				if operation == "revoke" {
					statement = `UPDATE caller_keys SET generation=generation+1,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL WHERE user_id=?`
				}
				if _, err := f.store.DB().Exec(statement, f.userID); err != nil {
					t.Fatal(err)
				}
				changed = true
			}}
			request := httptest.NewRequest("POST", "http://"+auditUserHost+"/v1/embeddings", body)
			request.Header.Set("Authorization", "Bearer "+f.caller)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			f.server.Config.Handler.ServeHTTP(response, request)
			if !changed || response.Code != http.StatusUnauthorized {
				t.Fatalf("stale identity reached acceptance: changed=%v status=%d body=%s", changed, response.Code, response.Body)
			}
			f.mu.Lock()
			calls := len(f.upstreamRequests)
			f.mu.Unlock()
			var accepted int
			if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM logical_requests WHERE accounting_state<>'none'`).Scan(&accepted); err != nil {
				t.Fatal(err)
			}
			if calls != 0 || accepted != 0 {
				t.Fatalf("revoked credential dispatched or reserved: calls=%d accepted=%d", calls, accepted)
			}
		})
	}
}
