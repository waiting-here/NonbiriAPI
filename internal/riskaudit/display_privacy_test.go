package riskaudit

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditDisplayKeepsEditableRuleValuesSeparate(t *testing.T) {
	value := map[string]string{"ip": "192.0.2.7", "note": "from 198.51.100.3"}
	for _, display := range []bool{true, false} {
		rec := httptest.NewRecorder()
		auditJSON(rec, 200, value, display)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), "192.0.2.7") == display {
			t.Fatalf("display=%v: %d %s", display, rec.Code, rec.Body)
		}
	}
	if value["ip"] != "192.0.2.7" {
		t.Fatal("source rule changed")
	}
}
