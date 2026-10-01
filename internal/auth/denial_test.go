package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func TestDenialGrantPaginatedCurrentReasonsAndCredentialIsolation(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	ctx := context.Background()
	discord := "123456789012345678"
	f.store.DB().Exec(`INSERT INTO discord_blacklist(discord_id,reason,created_at) VALUES(?,'Original manual reason',?)`, discord, authTestNow)
	for i := range 43 {
		if _, err := f.store.DB().Exec(`INSERT INTO discord_blacklist_events(discord_id,operation_key,actor_kind,reason_codes_json,safe_note,created_at) VALUES(?,?,'automatic','["deletion_penalty_evasion"]',?,?)`, discord, fmt.Sprintf("synthetic-operation-%d", i), fmt.Sprintf("Note %d\n完整理由", i), authTestNow+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	token, err := f.runtime.issueDenial(ctx, discord)
	if err != nil {
		t.Fatal(err)
	}
	grant := sessionCookie(DenialCookieName, token, denialCookiePath, true, 300, time.Unix(authTestNow+300, 0))
	var stored []byte
	if err = f.store.DB().QueryRow(`SELECT token_hash FROM auth_denial_grants`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(token))
	if string(stored) != string(hash[:]) || strings.Contains(string(stored), token) {
		t.Fatal("unsafe lookup material")
	}
	next := ""
	total := 0
	for pageNo := range 3 {
		path := "https://user.example/api/auth/access-denied-reasons"
		if next != "" {
			path += "?cursor=" + next
		}
		response := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, path, "", []*http.Cookie{grant}, nil)
		var page DenialPage
		if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != 200 || !page.Restricted || len(page.Items) > 20 || len(response.Body.Bytes()) > 128*1024 {
			t.Fatal(response.Code, response.Body.String(), err)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable denial")
		}
		if pageNo == 0 && page.Items[0].Reason != "Original manual reason" {
			t.Fatal(page)
		}
		for _, item := range page.Items {
			if strings.Contains(item.Reason, "Note 0") {
				t.Fatal("first event duplicated")
			}
		}
		total += len(page.Items)
		next = page.NextCursor
	}
	if total != 43 || next != "" {
		t.Fatal(total, next)
	}
	anonymous := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/access-denied-reasons?discord_id="+discord, "", nil, nil)
	if anonymous.Code != 400 {
		t.Fatal(anonymous.Code)
	}
	session := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{{Name: UserSessionCookieName, Value: token}}, nil)
	if session.Code != 401 {
		t.Fatal("grant gained session authority", session.Code)
	}
	f.store.DB().Exec(`DELETE FROM discord_blacklist WHERE discord_id=?`, discord)
	current := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/access-denied-reasons", "", []*http.Cookie{grant}, nil)
	var page DenialPage
	if err = json.Unmarshal(current.Body.Bytes(), &page); err != nil || page.Restricted || len(page.Items) != 0 {
		t.Fatal(current.Code, current.Body.String(), err)
	}
	expired := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/access-denied-reasons", "", []*http.Cookie{grant}, nil)
	if expired.Code != 401 {
		t.Fatal("released grant remained usable", expired.Code)
	}
}

func TestDenialLongestRuleFactsFitBudgetWithoutLosingManualHistory(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	loginUser(t, f, "initial", "")
	discord := "123456789012345678"
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id=?,lang='en',is_banned=1 WHERE discord_id='discord-1'`, discord); err != nil {
		t.Fatal(err)
	}
	var user int64
	if err := f.store.DB().QueryRow(`SELECT id FROM users WHERE discord_id=?`, discord).Scan(&user); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reason := observability.AutomaticReason{Kind: "client_rules", SchemaVersion: 1, Params: json.RawMessage(`{}`), ManualText: strings.Repeat("<", 4095) + "X"}
	for i := range 100 {
		reason.Rules = append(reason.Rules, observability.ReasonRuleLabel{RuleID: strings.Repeat("x", 26), Revision: int64(i + 1), Name: strings.Repeat("<", 120)})
	}
	if err = observability.PutAutomaticReasonTx(ctx, tx, "user_ban", strconv.FormatInt(user, 10), reason); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	raw, err := f.runtime.issueDenial(ctx, discord)
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/access-denied-reasons", "", []*http.Cookie{{Name: DenialCookieName, Value: raw}}, nil)
	var page DenialPage
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != 200 || len(response.Body.Bytes()) > 128*1024 || len(page.Items) != 1 {
		t.Fatal(response.Code, len(response.Body.Bytes()), err)
	}
	item := page.Items[0]
	if item.Metadata == nil || len(item.Metadata.Rules) != 100 || item.Metadata.ManualText != reason.ManualText || item.Reason != "Client usage rules were matched." {
		t.Fatal("lost rule labels, English projection, or manual history")
	}
	if strings.Contains(response.Body.String(), `"rule_id"`) || strings.Contains(response.Body.String(), `"revision"`) {
		t.Fatal("denial exposed management rule identifiers")
	}
}

func TestDenialCursorBoundToGrantExpiryAndLongReasonsBudget(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	discord := "123456789012345678"
	ctx := context.Background()
	if _, err := f.store.DB().Exec(`INSERT INTO discord_blacklist(discord_id,reason,created_at) VALUES(?,?,?)`, discord, strings.Repeat("<", 1999)+"X", authTestNow); err != nil {
		t.Fatal(err)
	}
	for i := range 25 {
		if _, err := f.store.DB().Exec(`INSERT INTO discord_blacklist_events(discord_id,operation_key,actor_kind,reason_codes_json,safe_note,created_at) VALUES(?,?,'automatic','["deletion_debt_evasion"]',?,?)`, discord, fmt.Sprint(i), strings.Repeat("<", 1999)+"X", authTestNow+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := f.runtime.issueDenial(ctx, discord)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: DenialCookieName, Value: raw}
	response := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/access-denied-reasons", "", []*http.Cookie{cookie}, nil)
	var page DenialPage
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != 200 || len(response.Body.Bytes()) > 128*1024 || page.NextCursor == "" || len(page.Items) >= 20 {
		t.Fatal(response.Code, len(response.Body.Bytes()), err)
	}
	other, err := f.runtime.issueDenial(ctx, discord)
	if err != nil {
		t.Fatal(err)
	}
	wrong := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/access-denied-reasons?cursor="+page.NextCursor, "", []*http.Cookie{{Name: DenialCookieName, Value: other}}, nil)
	if wrong.Code != 400 {
		t.Fatal("cursor escaped grant domain", wrong.Code)
	}
	f.clock.Add(5 * time.Minute)
	ended := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/access-denied-reasons", "", []*http.Cookie{cookie}, nil)
	if ended.Code != 401 {
		t.Fatal(ended.Code)
	}
	if result, err := f.runtime.RetainSessionsAt(ctx, f.clock.Now().Unix(), 10, time.Now().Add(time.Second)); err != nil || result.Processed != 2 || result.More {
		t.Fatal(result, err)
	}
}
