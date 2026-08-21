package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExportProjectionsOwnershipIsolation asserts that every export collection
// is scoped to one user in SQL: a second user's projections are empty and a
// cross-user id never leaks another user's rows.
func TestExportProjectionsOwnershipIsolation(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "export-own.db"))
	defer st.Close()
	ctx := context.Background()

	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	key, err := st.CreateEndpointKey(ctx, alice, aEP.ID, []byte("sk-export-ownership"), "h", "t", "note", true, 1)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if _, err := st.CreateModel(ctx, alice, "p", "m", "ordered", false, 1); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := st.RegenerateCallerKey(alice); err != nil {
		t.Fatalf("caller key: %v", err)
	}

	limit := ExportCollectionLimit
	if eps, err := st.ListExportEndpoints(ctx, alice, limit); err != nil || len(eps) != 1 {
		t.Fatalf("alice endpoints=%d err=%v", len(eps), err)
	}
	if eps, err := st.ListExportEndpoints(ctx, bob, limit); err != nil || len(eps) != 0 {
		t.Fatalf("bob endpoints=%d err=%v", len(eps), err)
	}
	if keys, err := st.ListExportEndpointKeys(ctx, alice, limit); err != nil || len(keys) != 1 || keys[0].ID != key.ID {
		t.Fatalf("alice keys=%d err=%v", len(keys), err)
	}
	if keys, err := st.ListExportEndpointKeys(ctx, bob, limit); err != nil || len(keys) != 0 {
		t.Fatalf("bob keys=%d err=%v", len(keys), err)
	}
	if models, err := st.ListExportModels(ctx, alice, limit); err != nil || len(models) != 1 {
		t.Fatalf("alice models=%d err=%v", len(models), err)
	}
	if models, err := st.ListExportModels(ctx, bob, limit); err != nil || len(models) != 0 {
		t.Fatalf("bob models=%d err=%v", len(models), err)
	}
	if key, err := st.GetCallerKey(alice); err != nil || key == nil {
		t.Fatalf("alice caller key=%#v err=%v", key, err)
	}
	if key, err := st.GetCallerKey(bob); err != nil || key != nil {
		t.Fatalf("bob caller key=%#v err=%v", key, err)
	}
	if summary, err := st.ExportLogSummaryForUser(ctx, bob); err != nil || summary.TotalLogs != 0 {
		t.Fatalf("bob log summary=%#v err=%v", summary, err)
	}
}

// TestExportEndpointKeysNeverProjectCiphertext asserts the export projection
// selects metadata/display fragments only: the sealed envelope (and the
// plaintext it protects) never leaves the row.
func TestExportEndpointKeysNeverProjectCiphertext(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "export-secret.db"))
	defer st.Close()
	ctx := context.Background()

	user := seedTestUser(t, st, "secret-user", nil)
	ep := mustCreateTestEndpoint(t, st, user, "https://upstream.example/v1/")
	plaintext := "sk-very-secret-upstream-material"
	if _, err := st.CreateEndpointKey(ctx, user, ep.ID, []byte(plaintext), "head", "tail", "note", true, 1); err != nil {
		t.Fatalf("create key: %v", err)
	}

	// Sanity: the row really does hold the envelope (the test is meaningful).
	var stored string
	if err := st.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_keys`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "nbsec:v2:aes-256-gcm:") || strings.Contains(stored, plaintext) {
		t.Fatalf("stored envelope sanity check failed: %q", stored)
	}
	ciphertext := stored

	keys, err := st.ListExportEndpointKeys(ctx, user, ExportCollectionLimit)
	if err != nil {
		t.Fatalf("export keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("export keys=%d", len(keys))
	}
	encoded, _ := json.Marshal(keys[0])
	for _, forbidden := range []string{ciphertext, plaintext, "nbsec:", "encrypted_secret"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("export projection leaked %q", forbidden)
		}
	}
}

// TestExportCollectionLimitFailsClosed asserts a projection that crosses its
// finite bound returns ErrExportLimit instead of a partial set.
func TestExportCollectionLimitFailsClosed(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "export-limit.db"))
	defer st.Close()
	ctx := context.Background()

	user := seedTestUser(t, st, "limit-user", nil)
	for i := 0; i < 4; i++ {
		if _, err := st.CreateEndpoint(ctx, user, "openai-compatible", "https://limit.example/v1/", "", true, 1); err != nil {
			t.Fatalf("create endpoint %d: %v", i, err)
		}
	}
	if eps, err := st.ListExportEndpoints(ctx, user, 3); !errors.Is(err, ErrExportLimit) {
		t.Fatalf("endpoints over limit: n=%d err=%v, want ErrExportLimit", len(eps), err)
	}
	// Exactly at the limit succeeds.
	if eps, err := st.ListExportEndpoints(ctx, user, 4); err != nil || len(eps) != 4 {
		t.Fatalf("endpoints at limit: n=%d err=%v", len(eps), err)
	}
	// Invalid limits fail closed too.
	if _, err := st.ListExportEndpoints(ctx, user, 0); !errors.Is(err, ErrExportLimit) {
		t.Fatalf("zero limit err=%v, want ErrExportLimit", err)
	}
	if _, err := st.ListExportEndpoints(ctx, user, ExportCollectionLimit+1); !errors.Is(err, ErrExportLimit) {
		t.Fatalf("over-max limit err=%v, want ErrExportLimit", err)
	}
}

// TestExportLogSummaryAggregates asserts the log summary counts and averages
// metadata-only aggregates for one user.
func TestExportLogSummaryAggregates(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "export-logs.db"))
	defer st.Close()
	ctx := context.Background()

	userID := seedUsageUser(t, st, "logs-user")
	keyID := seedUsageKey(t, st, userID)
	now := time.Now().UTC()
	attempt := 0
	makeInput := func(status int, durationMs int64, unknown bool, offset time.Duration) RequestLogInput {
		attempt++
		input := usageInput(userID, keyID)
		input.AttemptID = fmt.Sprintf("attempt-export-%d", attempt)
		input.StatusCode = status
		input.DurationMs = durationMs
		input.UsageUnknown = unknown
		if unknown {
			input.PromptTokens = 0
			input.CompletionTokens = 0
			input.TotalTokens = 0
		}
		input.StartedAt = now.Add(offset)
		input.CompletedAt = now.Add(offset).Add(time.Second)
		return input
	}
	// Two errors (one outside the 30-day window), one success, one
	// usage-unknown.
	if err := st.RecordRequest(ctx, makeInput(502, 150, false, 0)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRequest(ctx, makeInput(200, 50, false, -time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRequest(ctx, makeInput(200, 100, true, -2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordRequest(ctx, makeInput(500, 200, false, -40*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	summary, err := st.ExportLogSummaryForUser(ctx, userID)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.TotalLogs != 4 {
		t.Errorf("total=%d, want 4", summary.TotalLogs)
	}
	if summary.LogsLast30Days != 3 {
		t.Errorf("last30d=%d, want 3", summary.LogsLast30Days)
	}
	if summary.ErrorLogs != 2 {
		t.Errorf("errors=%d, want 2", summary.ErrorLogs)
	}
	if summary.UsageUnknownLogs != 1 {
		t.Errorf("unknown=%d, want 1", summary.UsageUnknownLogs)
	}
	// avg = (150+50+100+200)/4 = 125
	if summary.AvgDurationMs != 125 {
		t.Errorf("avg=%d, want 125", summary.AvgDurationMs)
	}
}

// TestExportBindingsOwnershipAndOrder asserts bindings are ownership-scoped
// and ordered by (ord, id) like the routing projection consumes them.
func TestExportBindingsOwnershipAndOrder(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "export-bind.db"))
	defer st.Close()
	ctx := context.Background()

	user := seedUserRaw(t, st, "bind-user")
	epID := seedEndpointRaw(t, st, user, true)
	keyID := seedEndpointKeyRaw(t, st, epID, true)
	seedFetchedModelRaw(t, st, keyID, "up-1")
	seedFetchedModelRaw(t, st, keyID, "up-2")
	modelID := seedModelRaw(t, st, user, "p", "m")
	if _, err := st.CreateBinding(ctx, user, modelID, keyID, "up-2", 2, 1); err != nil {
		t.Fatalf("binding up-2: %v", err)
	}
	if _, err := st.CreateBinding(ctx, user, modelID, keyID, "up-1", 1, 1); err != nil {
		t.Fatalf("binding up-1: %v", err)
	}

	bindings, err := st.ListExportBindings(ctx, user, ExportCollectionLimit)
	if err != nil {
		t.Fatalf("bindings: %v", err)
	}
	if len(bindings) != 2 || bindings[0].UpstreamModelID != "up-1" || bindings[1].UpstreamModelID != "up-2" {
		t.Fatalf("binding order=%+v", bindings)
	}
	other := seedUserRaw(t, st, "bind-other")
	if rows, err := st.ListExportBindings(ctx, other, ExportCollectionLimit); err != nil || len(rows) != 0 {
		t.Fatalf("cross-user bindings=%d err=%v", len(rows), err)
	}
}
