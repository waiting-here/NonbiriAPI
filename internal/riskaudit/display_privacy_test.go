package riskaudit

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditDisplayPreservesSavedFactsWithSafeJSONEncoding(t *testing.T) {
	value := map[string]string{"ip": "192.0.2.7", "note": "from 198.51.100.3 <script> & mail person@example.invalid " + strings.Repeat("x", 4096)}
	rec := httptest.NewRecorder()
	auditJSON(rec, 200, value)
	var got map[string]string
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got["ip"] != value["ip"] || got["note"] != value["note"] || strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("incomplete or unsafe display: %d %s", rec.Code, rec.Body)
	}
}
