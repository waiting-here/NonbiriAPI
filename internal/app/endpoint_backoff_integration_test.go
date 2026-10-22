package app

import (
	"bytes"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestEndpointBackoffHTTPDispatchClassifiesPaymentFailure(t *testing.T) {
	var upstreamCalls atomic.Int64
	f := newModelHTTPFixture(t, func(store *db.Store, base string) {
		_, err := store.DB().Exec(`INSERT INTO endpoint_backoff_policies(connector_type,base_url,revision,rules_json) VALUES('openai-compatible',?,1,'[{"kind":"code","value":"insufficient_credits","period":"auto"}]')`, base)
		if err != nil {
			t.Fatal(err)
		}
	}, "openai-compatible", func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer embedding-fixture-secret" {
			t.Error("unexpected upstream request", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":{"message":"Insufficient credits","type":"invalid_request_error","code":"insufficient_credits"}}`)
	})
	f.exec(t, `UPDATE site_config SET value='0' WHERE key='charity_min_chars'`)
	// This case proves the chat service's own backoff. Other native services
	// in a multi-service target may legitimately remain available for conversion.
	f.exec(t, `UPDATE charity_binding_targets SET native_service_mask=1`)
	streak := db.EncodeU128(db.U128{15: 2})
	health := appFixtureServiceHealth(t, f.store, f.keyID, connectorcontract.NativeServiceChatCompletions, time.Now().Unix())
	f.exec(t, `UPDATE donation_service_health SET failure_streak=? WHERE id=?`, streak, health)
	var donationKey int64
	if err := f.store.DB().QueryRow(`SELECT id FROM donation_keys WHERE endpoint_key_id=?`, f.keyID).Scan(&donationKey); err != nil {
		t.Fatal(err)
	}
	request := `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"hello"}]}`
	started := time.Now().Unix()
	status, body := f.post(t, "/v1/chat/completions", request)
	if status != http.StatusPaymentRequired || upstreamCalls.Load() != 1 {
		t.Fatalf("payment response: %d %s, upstream calls %d", status, body, upstreamCalls.Load())
	}
	projection := appFixtureServiceBackoff(t, f.store, health, time.Now().Unix())
	if projection.Backoff == nil {
		t.Fatal(projection)
	}
	backoff := projection.Backoff
	if backoff.State != "waiting" || backoff.Source != "custom_rule" || backoff.RetryAt < started+900 || backoff.RetryAt > time.Now().Unix()+900 {
		t.Fatalf("custom classification did not reach claim completion: %+v", backoff)
	}
	var actualStreak []byte
	if err := f.store.DB().QueryRow(`SELECT failure_streak FROM donation_service_health WHERE id=?`, health).Scan(&actualStreak); err != nil || !bytes.Equal(actualStreak, streak) {
		t.Fatal("custom match changed prior failure streak", actualStreak, err)
	}
	var revision, ruleIndex, upstreamStatus int
	var source, disposition string
	if err := f.store.DB().QueryRow(`SELECT f.policy_revision,f.rule_index,f.source,a.upstream_status,c.streak_disposition FROM dispatch_smart_backoff_facts f JOIN dispatch_claims c ON c.id=f.claim_id JOIN request_attempts a ON a.claim_id=c.id WHERE f.donation_key_id=?`, donationKey).Scan(&revision, &ruleIndex, &source, &upstreamStatus, &disposition); err != nil || revision != 1 || ruleIndex != 1 || source != "custom_rule" || upstreamStatus != 402 || disposition != "neutral" {
		t.Fatal("missing persisted dispatch proof", revision, ruleIndex, source, upstreamStatus, disposition, err)
	}
	status, body = f.post(t, "/v1/chat/completions", request)
	if status == http.StatusOK || upstreamCalls.Load() != 1 {
		t.Fatalf("waiting key dispatched again: %d %s, upstream calls %d", status, body, upstreamCalls.Load())
	}
}
