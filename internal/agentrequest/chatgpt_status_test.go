package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestChatGPTStatusCountsOnlyRetainedProviderSnapshots(t *testing.T) {
	f := newFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("status made an upstream request") })
	r := f.runtime
	denied := false
	forbidden := errors.New("administrator required")
	authorize := func(context.Context, *sql.Tx) error {
		if denied {
			return forbidden
		}
		return nil
	}
	m, err := chatgptauth.New(chatgptauth.Config{DB: r.db, ReadDB: r.readDB, Stack: r.stack, Secrets: r.secrets.(secret.ChatGPTCodec), AdminAuthorize: authorize, Now: r.now})
	if err != nil {
		t.Fatal(err)
	}
	r.chatgpt, r.adminAuthorize = m, authorize
	now := f.clock.Load()
	if _, err = f.database.Exec(`INSERT INTO agent_chatgpt_session(id,revision,host_id,public_key,encrypted_private_key,encrypted_credentials,expires_at,updated_at) VALUES(1,2,'fixture-host','public','private-ciphertext','credential-ciphertext',?,?)`, now/1000+3600, now-6000); err != nil {
		t.Fatal(err)
	}
	add := func(provider string, at int64, usage Usage, code string, status int, body string) int64 {
		t.Helper()
		result, err := f.database.Exec(`INSERT INTO agent_requests(client_request_id,source,kind,actor_id,actor_role,actor_label,input_json,state,created_at,completed_at,consumed_at,error_code,status_code,response_body) VALUES(?,'admin_chat','model',1,'admin','fixture','{}','failed',?,?,?, ?,?,?)`, fmt.Sprintf("request-%d-%s-%s", at, provider, code), at, at, at, code, status, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		if provider != "" {
			if _, err = f.database.Exec(`INSERT INTO agent_request_drivers(request_id,driver_id,provider,settings_json) VALUES(?,9,?,'{}')`, id, provider); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = f.database.Exec(`INSERT INTO agent_request_usage(request_id,client_request_id,source,kind,actor_id,actor_role,actor_label,recorded_at,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_tokens,unknown) VALUES(?,?,'admin_chat','model',1,'admin','fixture',?,?,?,?,?,?,?)`, id, fmt.Sprint(id), at, usage.InputTokens, usage.CachedInputTokens, usage.CacheWriteInputTokens, usage.OutputTokens, usage.ReasoningTokens, usage.Unknown); err != nil {
			t.Fatal(err)
		}
		return id
	}
	known := Usage{InputTokens: 100, CachedInputTokens: 25, OutputTokens: 20, ReasoningTokens: 4}
	unknown := Usage{Unknown: true}
	knownID := add(ProviderChatGPT, now-6000, known, "", 200, "")
	add(ProviderChatGPT, now-5000, unknown, "subscription_sharing_usage_limit_exceeded", 200, "data: synthetic stream")
	add(ProviderChatGPT, now-2000, unknown, "upstream_status", 403, `{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"secret upstream text"}}`)
	add(ProviderChatGPT, now-1000, unknown, "upstream_status", 429, `{"error":{"code":"rate_limit_exceeded"}}`)
	add(ProviderResponses, now-4000, known, "subscription_sharing_usage_limit_exceeded", 403, "")
	add("", now-3000, known, "", 200, "")
	add(ProviderChatGPT, now-(24*time.Hour).Milliseconds()-1, known, "", 200, "")
	add(ProviderChatGPT, now+1, known, "", 200, "")
	ctx := t.Context()
	status, err := r.ChatGPTStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := status.LocalUsage
	if u.Requests != 4 || u.UnknownCount != 3 || u.InputTokens != 100 || u.CachedInputTokens != 25 || u.OutputTokens != 20 || u.ReasoningTokens != 4 || u.WindowEnd != now || u.WindowStart != now-(24*time.Hour).Milliseconds() {
		t.Fatalf("wrong retained usage: %+v", u)
	}
	if status.LastLimitError == nil || status.LastLimitError.OccurredAt != now-2000 {
		t.Fatalf("wrong observed limit: %+v", status.LastLimitError)
	}
	encoded, _ := json.Marshal(status)
	for _, value := range []string{"private-ciphertext", "credential-ciphertext", "secret upstream text"} {
		if strings.Contains(string(encoded), value) {
			t.Fatal("status exposed private data")
		}
	}
	// Replacing the current account does not relabel old instance requests as
	// usage belonging to the new account, or remove them from this scope.
	if _, err = f.database.Exec(`UPDATE agent_chatgpt_session SET revision=revision+1,account_label='another account',updated_at=?`, now); err != nil {
		t.Fatal(err)
	}
	after, err := r.ChatGPTStatus(ctx)
	if err != nil || after.LocalUsage != u || after.Revision != status.Revision+1 {
		t.Fatal(after, err)
	}
	if _, err = r.Cleanup(ctx, CleanupInput{IDs: []string{fmt.Sprint(knownID)}}); err != nil {
		t.Fatal(err)
	}
	after, err = r.ChatGPTStatus(ctx)
	if err != nil || after.LocalUsage.Requests != 3 || after.LocalUsage.InputTokens != 0 || after.LocalUsage.UnknownCount != 3 {
		t.Fatal(after, err)
	}
	denied = true
	if _, err = r.ChatGPTStatus(ctx); !errors.Is(err, forbidden) {
		t.Fatal("unauthorized status", err)
	}
}
