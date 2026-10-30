package diagnostic

import (
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOperationDiagnosticStorageIsBoundedAndFailureIndependent(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(t.TempDir(), "diagnostics.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ddl, err := os.ReadFile("../db/agent_operation_diagnostics.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	ctx := WithRecorder(t.Context())
	cause := errors.New("private arbitrary error text")
	RecordKnownError(ctx, "credential", "platform", "credential_unreadable", "Credential could not be opened", cause)
	failure := OperationFailure(ctx, "model_catalogue", strings.Repeat("source", 100), cause)
	SaveOperation(t.Context(), database, failure)
	if !failure.Saved || len(failure.SourceName) > 200 || !errors.Is(failure, cause) {
		t.Fatal(failure)
	}
	read, err := ReadOperation(t.Context(), database, failure.ID)
	if err != nil || read.Details.Code != "credential_unreadable" {
		t.Fatal(read, err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 1001 {
		if _, err = tx.Exec("INSERT INTO agent_operation_diagnostics(id,operation,source_name,site_status,details,created_at) VALUES(?,'codex_quota','Codex',502,'{}',?)", fmt.Sprintf("%032x", i), time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = RetainOperations(t.Context(), database, time.Now()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = database.QueryRow("SELECT count(*) FROM agent_operation_diagnostics").Scan(&count); err != nil || count != 1000 {
		t.Fatal(count, err)
	}
	database.Close()
	second := OperationFailure(WithRecorder(t.Context()), "codex_quota", "Codex", cause)
	SaveOperation(t.Context(), database, second)
	if second.Saved || second.ID == "" || !errors.Is(second, cause) {
		t.Fatal("storage failure replaced original error")
	}
}

func TestOperationDetailAndCorrelationRejectCredentialEcho(t *testing.T) {
	retry := int64(123456)
	observation := &Failure{RetryAfterSeconds: &retry}
	DropReflectedCorrelation(observation, []byte("123456"))
	if observation.RetryAfterSeconds != nil {
		t.Fatal("numeric OAuth secret reflected as Retry-After")
	}
	secret := []byte("private-token-fixture")
	ctx := WithRecorder(t.Context())
	RecordResponse(ctx, 403, []Correlation{{Name: "x-request-id", Value: string(secret)}, {Name: "cf-ray", Value: "safe-id"}})
	code, err := RecordHTTPFailure(ctx, 403, []byte(`{"error":{"code":"private-token-fixture","message":"Bearer private-token-fixture email@example.invalid"}}`), func(value []byte) bool { return ReflectsSecret(value, secret) })
	failure := OperationFailure(ctx, "model_catalogue", "Source", err)
	failure.UpstreamCode = code
	DropReflectedCorrelation(failure.Details, secret)
	raw := Encode(failure.Details)
	if strings.Contains(raw, string(secret)) || strings.Contains(raw, "email@example.invalid") || code != "" || len(failure.Details.Correlation) != 1 {
		t.Fatal(raw, code)
	}
	if failure.Details.Code == "upstream_account_restricted" {
		t.Fatal("403 inferred account restriction")
	}
}
