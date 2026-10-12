package auth

import (
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestRedemptionRestrictionUsesLocalizedReasonAndExpires(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	loginUser(t, f, "initial", "")
	var user, at int64
	if err := f.store.DB().QueryRow("SELECT id,created_at FROM users WHERE discord_id='discord-1'").Scan(&user, &at); err != nil {
		t.Fatal(err)
	}
	id, err := db.GenerateOpaqueID("abc_")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE users SET is_banned=1,auto_banned=1,banned_until=? WHERE id=?", at+100, user); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO abuse_cases(id,user_id,kind,reason_code,started_at,ends_at,state,result) VALUES(?,?,'ban','redemption_code',?,?,'active','applied')", id, user, at, at+100); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"zh", "en"} {
		items, err := readAutomaticRestrictions(t.Context(), tx, user, at, lang)
		if err != nil || len(items) != 1 || items[0].ReasonCode != "redemption_code" {
			t.Fatal(items, err)
		}
		if lang == "zh" && !strings.Contains(items[0].Reason, "兑换码") || lang == "en" && !strings.Contains(items[0].Reason, "redemption codes") {
			t.Fatal(items[0].Reason)
		}
		if strings.Contains(items[0].Reason, "公益") || strings.Contains(items[0].Reason, "Charity") {
			t.Fatal("wrong punishment reason")
		}
	}
	items, err := readAutomaticRestrictions(t.Context(), tx, user, at+100, "en")
	if err != nil || len(items) != 0 {
		t.Fatal("expired automatic restriction shown", items, err)
	}
}
