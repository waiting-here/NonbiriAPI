package db

import (
	"encoding/json"
	"testing"
)

func TestScanCheckpointPrivacyWithoutPublishedResults(t *testing.T) {
	for _, change := range []string{"source_delete", "source_retire", "user_delete", "authority_change"} {
		for _, cursor := range []string{"pending", "after"} {
			t.Run(change+"/"+cursor, func(t *testing.T) {
				database := openGenerationTwoDDLForTest(t)
				defer database.Close()
				actor := hostileInsertUser(t, database, "scan owner", 0, 1)
				hostileMustExec(t, database, `UPDATE users SET level=6 WHERE id=?`, actor)
				victim := hostileInsertUser(t, database, "scan subject", 0, 1)
				var log int64
				if change == "source_delete" || change == "source_retire" {
					req := hostileOID("req_")
					hostileInsertLogicalRequest(t, database, req, victim, "openai_chat_completions", 1)
					log = hostileInsertRequestLog(t, database, req, victim, "openai_chat_completions")
					hostileMustExec(t, database, `INSERT INTO request_source_facts VALUES(?,?,'self','192.0.2.4','direct_peer','{}',1)`, log, victim)
				}
				checkpoint := map[string]any{"signal": "high_rpm", "config": map[string]any{"rpm": 10}, "source_at": 1, "source_id": 7}
				if log != 0 {
					checkpoint[cursor+"_ip"] = "192.0.2.4"
					checkpoint["ip_summary"] = map[string]any{"ip": "192.0.2.4", "users": []int64{victim}}
				} else {
					checkpoint[cursor+"_user"] = victim
				}
				raw, err := json.Marshal(checkpoint)
				if err != nil {
					t.Fatal(err)
				}
				scan := hostileOID("scn_")
				hostileMustExec(t, database, `INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,created_at,updated_at,expires_at,kind,checkpoint_json) VALUES(?,?,0,'abcdefghijklmnop','{}','[]','running',0,100,'total','',?,0,1,0,0,86400,'shared_ips',?)`, scan, actor, log, string(raw))
				switch change {
				case "source_delete":
					hostileMustExec(t, database, `DELETE FROM request_source_facts WHERE request_log_id=?`, log)
				case "source_retire":
					hostileMustExec(t, database, `UPDATE request_source_facts SET user_id=NULL WHERE request_log_id=?`, log)
				case "user_delete":
					hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, victim)
				case "authority_change":
					hostileMustExec(t, database, `UPDATE users SET level=5 WHERE id=?`, actor)
				}
				var state, reason, cleaned string
				var changed int
				if err = database.QueryRow(`SELECT state,reason,changed,checkpoint_json FROM risk_client_scans WHERE id=?`, scan).Scan(&state, &reason, &changed, &cleaned); err != nil {
					t.Fatal(err)
				}
				if change == "authority_change" {
					if state != "cancelled" || reason != "permission_changed" {
						t.Fatalf("authority invalidation: %s/%s", state, reason)
					}
				} else if state != "failed" || reason != "source_changed" || changed != 1 {
					t.Fatalf("source invalidation: %s/%s, changed=%d", state, reason, changed)
				}
				if cleaned != `{"config":{"rpm":10},"signal":"high_rpm"}` {
					t.Fatalf("retained identity or lost query metadata: %s", cleaned)
				}
			})
		}
	}
}
