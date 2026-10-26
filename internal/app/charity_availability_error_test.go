package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestHTTPCharityUnavailableServiceIsNotInvalidInput(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	f.exec(t, "UPDATE site_config SET value='1' WHERE key='charity_min_chars'")
	// Keep Chat and Embeddings authorized in the same target. A healthy
	// embedding service must not turn unavailable Chat capacity into bad input.
	f.exec(t, "UPDATE charity_binding_targets SET native_service_mask=17")
	for _, scenario := range []string{"disabled", "cooldown", "no services", "exhausted"} {
		t.Run(scenario, func(t *testing.T) {
			f.exec(t, "UPDATE donation_uses SET enabled=1,revision=revision+1")
			f.exec(t, "UPDATE donation_keys SET call_limit_mag=NULL")
			f.exec(t, "DELETE FROM donation_service_health")
			switch scenario {
			case "disabled":
				f.exec(t, "UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE native_service_bit=1")
			case "cooldown":
				f.exec(t, `INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,retry_after_until,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT a.use_id,dk.endpoint_key_id,a.native_service_bit,a.credential_version_id,nbi_u128(1),nbi_u128(0),nbi_u128(1),nbi_u128(1),?,1,0,0,? FROM charity_use_availability a JOIN donation_keys dk ON dk.id=a.donation_key_id WHERE a.native_service_bit=1`, time.Now().Unix()+3600, time.Now().Unix())
			case "no services":
				f.exec(t, "UPDATE donation_uses SET enabled=0,revision=revision+1")
			case "exhausted":
				f.exec(t, "UPDATE donation_keys SET call_limit_mag=nbi_u128(0)")
			}
			r, err := http.NewRequest(http.MethodPost, f.server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"[公益]provider/per_request","stream":true,"messages":[{"role":"user","content":"PRIVATE_INPUT"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			r.Host = auditUserHost
			r.Header.Set("Authorization", "Bearer "+f.caller)
			r.Header.Set("Content-Type", "application/json")
			response, err := f.server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			var envelope httperr.Envelope
			if response.StatusCode != http.StatusServiceUnavailable || json.Unmarshal(body, &envelope) != nil || envelope.Error.Code != httperr.CodeUnboundModel {
				t.Fatalf("unavailable service: status=%d body=%s", response.StatusCode, body)
			}
			var detail string
			var attempts, charges int
			id := response.Header.Get("X-Request-ID")
			if err := f.store.DB().QueryRow(`SELECT error_diag,attempt_count,(SELECT count(*) FROM credit_operations WHERE source_type='logical_request' AND source_id=logical_request_id) FROM request_logs WHERE logical_request_id=?`, id).Scan(&detail, &attempts, &charges); err != nil {
				t.Fatal(err)
			}
			decoded := requestattempt.DecodeDetail(detail)
			if attempts != 0 || charges != 0 || decoded == nil || decoded.Suggestion == "" || !strings.Contains(envelope.Error.Message, decoded.Reason) {
				t.Fatalf("lost reason or charged rejection: attempts=%d charges=%d detail=%s response=%s", attempts, charges, detail, body)
			}
			for _, private := range []string{"PRIVATE_INPUT", "embedding-fixture-secret", "private-model", f.caller} {
				if strings.Contains(string(body)+detail, private) {
					t.Fatalf("private data exposed: %s", private)
				}
			}
		})
	}
	f.mu.Lock()
	calls := len(f.upstreamRequests)
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("unavailable service dispatched", calls)
	}
	f.exec(t, "UPDATE donation_uses SET enabled=1,revision=revision+1")
	f.exec(t, "UPDATE donation_keys SET call_limit_mag=NULL")
	status, body := f.post(t, "/v1/chat/completions", `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"valid complete request"}]}`)
	if status != http.StatusOK {
		t.Fatalf("restored service: %d %s", status, body)
	}
}
