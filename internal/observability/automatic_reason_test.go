package observability

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestAutomaticReasonRetainsAllLabelsManualTextAndEnglish(t *testing.T) {
	r, user := newObservedDatabase(t)
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reason := AutomaticReason{Kind: "client_rules", SchemaVersion: 1, Params: json.RawMessage(`{}`), ManualText: "Unchanged manual fact\n原始人工理由"}
	for i := range 100 {
		reason.Rules = append(reason.Rules, ReasonRuleLabel{RuleID: strings.Repeat("x", 26), Revision: int64(i + 1), Name: strings.Repeat("长", 120)})
	}
	if err = PutAutomaticReasonTx(context.Background(), tx, "user_ban", strconv.FormatInt(user, 10), reason); err != nil {
		t.Fatal(err)
	}
	stored, err := ReadAutomaticReasonTx(context.Background(), tx, "user_ban", strconv.FormatInt(user, 10))
	if err != nil || stored == nil || len(stored.Rules) != 100 {
		t.Fatal(stored, err)
	}
	if !strings.HasPrefix(stored.Text("en"), "Detected prohibited third-party client characteristics: ") || !strings.HasSuffix(stored.Text("en"), reason.ManualText) || strings.Count(stored.Text("en"), strings.Repeat("长", 120)) != 100 {
		t.Fatal("labels or manual history lost")
	}
}
