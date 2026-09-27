package requestadaptation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:request-adaptation-"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE endpoints(id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TABLE request_adaptation_audits (
id INTEGER PRIMARY KEY,scope TEXT NOT NULL,resource_id INTEGER NOT NULL,
actor_user_id INTEGER,actor_role TEXT NOT NULL,revision INTEGER NOT NULL,
changed_partitions TEXT NOT NULL,created_at INTEGER NOT NULL,
UNIQUE(scope,resource_id,revision)) STRICT`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	_, err = database.Exec(`CREATE TABLE request_adaptations (
	 id INTEGER PRIMARY KEY, scope TEXT NOT NULL,
	 endpoint_id INTEGER UNIQUE, model_id INTEGER UNIQUE, binding_id INTEGER UNIQUE,
	 revision INTEGER NOT NULL, secret_context BLOB NOT NULL, secret_ciphertext TEXT NOT NULL,
	 structure_json TEXT NOT NULL, actor_user_id INTEGER, updated_at INTEGER NOT NULL
	) STRICT`)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := secret.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	store, err := New(Config{DB: database, Codec: vault, KeyDeriver: vault})
	if err != nil {
		t.Fatal(err)
	}
	return store, database
}

func TestEncryptedRevisionAndRedactedProjection(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()
	ref := Ref{Scope: ScopeEndpoint, ID: 42}
	doc := Empty(ScopeEndpoint)
	doc.FixedHeaders.Values["X-Access"] = "SECRET-header-value"
	doc.BodyForced.Values["/thinking"] = json.RawMessage(`{"budget_tokens":2048,"secret":"SECRET-body-value"}`)
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := store.SaveTx(ctx, tx, ref, 0, doc, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(projection)
	if strings.Contains(string(encoded), "SECRET") || !strings.Contains(string(encoded), "has_value") {
		t.Fatalf("unsafe projection: %s", encoded)
	}
	var cipher, structure string
	if err := database.QueryRow(`SELECT secret_ciphertext,structure_json FROM request_adaptations WHERE endpoint_id=42`).Scan(&cipher, &structure); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cipher+structure, "SECRET") || !strings.HasPrefix(cipher, "nbsec:v2:") {
		t.Fatalf("plaintext persisted or wrong envelope: %s", structure)
	}
	var actorRole, changedPartitions string
	if err := database.QueryRow(`SELECT actor_role,changed_partitions FROM request_adaptation_audits WHERE scope='endpoint' AND resource_id=42 AND revision=1`).Scan(&actorRole, &changedPartitions); err != nil || actorRole != "owner" || changedPartitions != `["fixed_headers","body_forced"]` {
		t.Fatalf("audit metadata = (%q,%q), %v", actorRole, changedPartitions, err)
	}
	got, err := store.Load(ctx, ref)
	if err != nil || got.Document.FixedHeaders.Values["X-Access"] != "SECRET-header-value" || got.Revision != "1" {
		t.Fatalf("load: %+v %v", got.Projection(), err)
	}
	tx, err = database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveTx(ctx, tx, ref, 0, doc, 1, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	_ = tx.Rollback()
	// Even a valid envelope transplanted to a different scope/id is rejected.
	if _, err := database.Exec(`UPDATE request_adaptations SET endpoint_id=43 WHERE endpoint_id=42`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, Ref{Scope: ScopeEndpoint, ID: 43}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("transplant: %v", err)
	}
}

func TestAdaptationAuditRetentionIsBoundedAcrossScopes(t *testing.T) {
	store, database := testStore(t)
	const now int64 = 20_000_000
	cutoff := now - auditRetentionSeconds
	for _, row := range []struct {
		scope string
		id    int64
		at    int64
	}{
		{"endpoint", 1, cutoff - 1},
		{"charity_model", 2, cutoff - 1},
		{"binding", 3, cutoff},
		{"binding", 4, cutoff + 1},
	} {
		if _, err := database.Exec(`INSERT INTO request_adaptation_audits(scope,resource_id,actor_user_id,actor_role,revision,changed_partitions,created_at) VALUES(?,?,1,'admin',1,'[]',?)`, row.scope, row.id, row.at); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Minute)
	first, err := store.Retain(context.Background(), now, 1, deadline)
	if err != nil || first.Processed != 1 || !first.More {
		t.Fatalf("first batch = %+v, %v", first, err)
	}
	second, err := store.Retain(context.Background(), now, 1, deadline)
	if err != nil || second.Processed != 1 || !second.More {
		t.Fatalf("second batch = %+v, %v", second, err)
	}
	var remaining int
	if err := database.QueryRow(`SELECT COUNT(*) FROM request_adaptation_audits`).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("remaining audits = %d, %v", remaining, err)
	}
	third, err := store.Retain(context.Background(), now, 1, deadline)
	if err != nil || third.Processed != 1 || third.More {
		t.Fatalf("third batch = %+v, %v", third, err)
	}
	fourth, err := store.Retain(context.Background(), now, 1, deadline)
	if err != nil || fourth.Processed != 0 || fourth.More {
		t.Fatalf("empty batch = %+v, %v", fourth, err)
	}
}

func TestOwnerExportAndDeleteAreValueFreeAndTransactionScoped(t *testing.T) {
	store, database := testStore(t)
	ctx := context.Background()
	if _, err := database.Exec(`INSERT INTO endpoints(id,user_id) VALUES(10,1),(20,2)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{10, 20} {
		doc := Empty(ScopeEndpoint)
		doc.ForwardHeaders.Values = []string{"X-Client"}
		doc.FixedHeaders.Values["X-Secret"] = "private-value"
		doc.BodyForced.Values["/thinking"] = json.RawMessage(`{"private":"secret-body"}`)
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SaveTx(ctx, tx, Ref{Scope: ScopeEndpoint, ID: id}, 0, doc, id/10, 100); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := store.ExportRequestAdaptations(ctx, tx, lifecycle.ExportRequest{UserID: 1, Limit: 1})
	if err != nil || len(exported) != 1 || exported[0].EndpointID != "10" || exported[0].FixedHeaders[0].Path != "X-Secret" || !exported[0].FixedHeaders[0].HasValue {
		t.Fatalf("owner export: %+v, %v", exported, err)
	}
	encoded, _ := json.Marshal(exported)
	if strings.Contains(string(encoded), "private-value") || strings.Contains(string(encoded), "secret-body") || strings.Contains(string(encoded), "20") {
		t.Fatalf("export leaked sensitive or cross-owner data: %s", encoded)
	}
	if _, err := store.PrepareDelete(ctx, tx, lifecycle.DeleteRequest{UserID: 1, Source: lifecycle.DeleteSelf}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM request_adaptation_audits`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback erased audits: %d, %v", count, err)
	}
	tx, err = database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareDelete(ctx, tx, lifecycle.DeleteRequest{UserID: 1, Source: lifecycle.DeleteSelf}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM request_adaptation_audits`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("delete scope: %d, %v", count, err)
	}
}

func TestEffectivePartitionReplaceAndClear(t *testing.T) {
	model := Snapshot{Document: Empty(ScopeCharityModel), Revision: "2"}
	model.Document.FixedHeaders.Values["X-A"] = "secret"
	model.Document.BodyDefaults.Values["/one"] = json.RawMessage(`1`)
	binding := Snapshot{Document: Empty(ScopeBinding), Revision: "3"}
	binding.Document.FixedHeaders.Mode = ModeReplace // Empty replace clears.
	binding.Document.BodyForced.Mode = ModeReplace
	binding.Document.BodyForced.Values["/two"] = json.RawMessage(`2`)
	effective := Effective(model, binding)
	if len(effective.Document.FixedHeaders.Values) != 0 || string(effective.Document.BodyDefaults.Values["/one"]) != "1" || string(effective.Document.BodyForced.Values["/two"]) != "2" || effective.Sources[1] != ScopeBinding || effective.Sources[2] != ScopeCharityModel {
		t.Fatalf("incorrect effective partition: %+v", effective.Projection())
	}
}
