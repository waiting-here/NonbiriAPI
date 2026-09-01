package issues

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLifecycleIssueExportRetentionAndLimit(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "lifecycle-issues")
	firstEndpoint := environment.seedEndpoint(t, userID)
	environment.validation.set(userID, ResourceEndpoint, firstEndpoint, RootCredentialInvalid, ResourceValidationState{
		Active: true, ObservedAt: issueTestNow, SafeDetail: "safe first detail",
	})
	if err := environment.service.ReconcileResourceValidation(
		context.Background(), userID, ResourceEndpoint, firstEndpoint, RootCredentialInvalid,
	); err != nil {
		t.Fatal(err)
	}

	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := environment.service.Sources().ExportLifecycleIssues(context.Background(), tx, userID, issueTestNow, 1)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if len(items) != 1 || items[0].State != "current" || items[0].DeepLink == nil {
		t.Fatalf("current lifecycle issues=%+v", items)
	}

	closedAt := issueTestNow + 1
	environment.clock.Store(closedAt)
	environment.validation.set(userID, ResourceEndpoint, firstEndpoint, RootCredentialInvalid, ResourceValidationState{
		Active: false, ObservedAt: closedAt,
	})
	if err := environment.service.ReconcileResourceValidation(
		context.Background(), userID, ResourceEndpoint, firstEndpoint, RootCredentialInvalid,
	); err != nil {
		t.Fatal(err)
	}
	secondEndpoint := environment.seedEndpoint(t, userID)
	environment.validation.set(userID, ResourceEndpoint, secondEndpoint, RootConfigurationInvalid, ResourceValidationState{
		Active: true, ObservedAt: closedAt, SafeDetail: "safe second detail",
	})
	if err := environment.service.ReconcileResourceValidation(
		context.Background(), userID, ResourceEndpoint, secondEndpoint, RootConfigurationInvalid,
	); err != nil {
		t.Fatal(err)
	}

	insideWindow := closedAt + closedRetention - 1
	tx, err = environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.Sources().ExportLifecycleIssues(context.Background(), tx, userID, insideWindow, 1); !errors.Is(err, ErrResourceLimit) {
		_ = tx.Rollback()
		t.Fatalf("limit+1 error=%v", err)
	}
	_ = tx.Rollback()
	tx, err = environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err = environment.service.Sources().ExportLifecycleIssues(context.Background(), tx, userID, insideWindow, 2)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if len(items) != 2 {
		t.Fatalf("inside-window issue count=%d", len(items))
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"ResourceRef", "RootCause", "Generation", "RetainUntil", "revision"} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("forbidden issue field %q in %s", forbidden, encoded)
		}
	}

	atBoundary := closedAt + closedRetention
	tx, err = environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err = environment.service.Sources().ExportLifecycleIssues(context.Background(), tx, userID, atBoundary, 1)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if len(items) != 1 || items[0].State != "current" || items[0].SummaryCode != string(RootConfigurationInvalid) {
		t.Fatalf("boundary lifecycle issues=%+v", items)
	}
}
