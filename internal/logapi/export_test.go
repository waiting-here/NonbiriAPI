package logapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAdminExportGoldenOrderingFiltersAndPrivacy(t *testing.T) {
	fixture := newLogFixture(t)
	rows, err := fixture.repo.ExportAdmin(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("ExportAdmin: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("export rows = %d, want 6", len(rows))
	}
	wantIDs := []string{
		fixture.selfID, fixture.charityID, fixture.discoveryID,
		fixture.otherID, fixture.deletedID, fixture.pendingID,
	}
	for index, want := range wantIDs {
		if rows[index].ID != want {
			t.Fatalf("export row %d ID = %q, want %q", index, rows[index].ID, want)
		}
	}
	jsonBody, err := MarshalAdminJSON(rows)
	if err != nil {
		t.Fatalf("MarshalAdminJSON: %v", err)
	}
	csvBody, err := MarshalAdminCSV(rows)
	if err != nil {
		t.Fatalf("MarshalAdminCSV: %v", err)
	}
	assertLogGolden(t, "admin_export.json.golden", jsonBody)
	assertLogGolden(t, "admin_export.csv.golden", csvBody)
	noLogSentinel(t, rows, "RAW-REQUEST-BODY", "RAW-AUTH", "RAW-COOKIE", "RAW-DISCORD",
		"RAW-PRIVATE-NOTE", "RAW-CIPHERTEXT", "RAW-UPSTREAM", "RAW-RESPONSE-BODY", "RAW-SET-COOKIE",
		"SELF-LOGICAL-MODEL", "OWNER-ENDPOINT-NOTE", "OWNER-KEY-NOTE")

	parsed, err := csv.NewReader(strings.NewReader(string(csvBody))).ReadAll()
	if err != nil {
		t.Fatalf("read export CSV: %v", err)
	}
	if len(parsed) != len(rows)+1 || len(parsed[0]) != 16 {
		t.Fatalf("CSV dimensions = %dx%d", len(parsed), len(parsed[0]))
	}

	endpoint := "https://charity-two.example/v1"
	filtered, err := fixture.repo.ExportAdmin(context.Background(), ListFilter{EndpointBaseURL: &endpoint})
	if err != nil || len(filtered) != 1 || filtered[0].ID != fixture.charityID {
		t.Fatalf("attempt-filtered export = (%+v,%v)", filtered, err)
	}
	status := 429
	filtered, err = fixture.repo.ExportAdmin(context.Background(), ListFilter{Status: &status})
	if err != nil || len(filtered) != 0 {
		t.Fatalf("attempt status leaked into logical export filter = (%+v,%v)", filtered, err)
	}
}

func TestAdminExportRejectsNonExportFieldsAndBounds(t *testing.T) {
	fixture := newLogFixture(t)
	model := "private-model"
	for name, filter := range map[string]ListFilter{
		"cursor": {Cursor: "cursor"},
		"limit":  {Limit: 1},
		"model":  {Model: &model},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fixture.repo.ExportAdmin(context.Background(), filter); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ExportAdmin error = %v", err)
			}
		})
	}
	if _, err := fixture.repo.ExportAdmin(nil, ListFilter{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil context export = %v", err)
	}
	tooMany := make([]AdminLogRow, maxExportRows+1)
	if _, err := MarshalAdminJSON(tooMany); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized JSON rows = %v", err)
	}
	if _, err := MarshalAdminCSV(tooMany); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized CSV rows = %v", err)
	}
	large := []AdminLogRow{{ID: strings.Repeat("x", maxExportBytes+1)}}
	if _, err := MarshalAdminJSON(large); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized JSON bytes = %v", err)
	}
	if _, err := MarshalAdminCSV(large); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized CSV bytes = %v", err)
	}
	baseline, err := json.Marshal(AdminLogExport{Data: []AdminLogRow{{}}})
	if err != nil {
		t.Fatal(err)
	}
	exactBeforeNewline := []AdminLogRow{{ID: strings.Repeat("x", maxExportBytes-len(baseline))}}
	if encoded, marshalErr := json.Marshal(AdminLogExport{Data: exactBeforeNewline}); marshalErr != nil || len(encoded) != maxExportBytes {
		t.Fatalf("exact JSON boundary fixture = %d bytes, err=%v", len(encoded), marshalErr)
	}
	if _, err := MarshalAdminJSON(exactBeforeNewline); !errors.Is(err, ErrCapacity) {
		t.Fatalf("JSON newline crossed byte boundary: %v", err)
	}
	exactJSON := []AdminLogRow{{ID: strings.Repeat("x", maxExportBytes-len(baseline)-1)}}
	jsonBody, err := MarshalAdminJSON(exactJSON)
	if err != nil || len(jsonBody) != maxExportBytes {
		t.Fatalf("exact JSON response boundary = %d bytes, err=%v", len(jsonBody), err)
	}

	csvBaseline, err := MarshalAdminCSV([]AdminLogRow{{}})
	if err != nil {
		t.Fatalf("CSV boundary baseline: %v", err)
	}
	exactCSV := []AdminLogRow{{ID: strings.Repeat("x", maxExportBytes-len(csvBaseline))}}
	csvBody, err := MarshalAdminCSV(exactCSV)
	if err != nil || len(csvBody) != maxExportBytes {
		t.Fatalf("exact CSV response boundary = %d bytes, err=%v", len(csvBody), err)
	}
	overCSV := []AdminLogRow{{ID: strings.Repeat("x", maxExportBytes-len(csvBaseline)+1)}}
	if _, err := MarshalAdminCSV(overCSV); !errors.Is(err, ErrCapacity) {
		t.Fatalf("CSV crossed byte boundary: %v", err)
	}
}

func TestCSVFormulaInjectionDefense(t *testing.T) {
	for _, prefix := range []string{"=", "+", "-", "@", "\t", "\r", "\n"} {
		value := prefix + "1+1"
		if got := csvSafe(value); got != "'"+value {
			t.Fatalf("csvSafe(%q) = %q", value, got)
		}
	}
	for _, value := range []string{"", "safe", "  =not-leading"} {
		if got := csvSafe(value); got != value {
			t.Fatalf("csvSafe(%q) = %q", value, got)
		}
	}
}
