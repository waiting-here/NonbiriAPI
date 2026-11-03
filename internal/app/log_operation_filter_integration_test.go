package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLogOperationFilterLiveRoles(t *testing.T) {
	f := newGameWireFixture(t)
	now := time.Now().Unix()
	for i, kind := range []string{"openai_responses", "charity_responses", "charity_embeddings"} {
		request := fmt.Sprintf("req_%021dA", 9000+i)
		if _, err := f.store.DB().Exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
VALUES(?,?,?,'model','terminal',1,'success',200,'none','user',zeroblob(16),?,?)`, request, f.userID, kind, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DB().Exec(`INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code)
VALUES(?,?,?,?,?,'success',200,200)`, request, f.userID, kind, now, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, level := range []int{1, 5, 6} {
		if _, err := f.store.DB().Exec(`UPDATE users SET level=? WHERE id=?`, level, f.userID); err != nil {
			t.Fatal(err)
		}
		for _, prefix := range []string{"/api/logs", "/api/steward/logs", "/admin/api/logs"} {
			host, cookies := auditUserHost, f.cookies
			if prefix == "/admin/api/logs" {
				host, cookies = auditAdminHost, f.adminCookies
			}
			for _, suffix := range []string{"", "/export.json", "/export.csv"} {
				response := testApplicationRequest(t, f.app.handler, "GET", host, prefix+suffix+"?operation=responses", "", cookies, nil)
				if prefix == "/api/steward/logs" && level == 1 {
					if response.Code != 403 {
						t.Fatal("steward permission bypassed", response.Code)
					}
					continue
				}
				body := response.Body.String()
				if response.Code != 200 || !strings.Contains(body, "openai_responses") || !strings.Contains(body, "charity_responses") || strings.Contains(body, "charity_embeddings") {
					t.Fatalf("level=%d %s%s: %d %s", level, prefix, suffix, response.Code, body)
				}
			}
		}
	}
}
