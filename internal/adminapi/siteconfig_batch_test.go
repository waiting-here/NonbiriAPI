package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
)

func TestConfigurationBatchIsAtomicRevisionCheckedAndReplayable(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	authorizer := &siteConfigTestFinalAuthorizer{}
	repository := newSiteConfigTestRepository(t, store, authorizer)
	initial := siteConfigRevision(t, store)
	input := SiteConfigBatchInput{AdminID: 1, ExpectedRevision: strconv.FormatInt(initial, 10), IdempotencyKey: "configuration-batch-0000001", Values: map[string]json.RawMessage{
		KeyCharityEnabled: json.RawMessage(`true`), KeyDonationAcceptEnabled: json.RawMessage(`true`),
	}}
	if _, err := store.DB().Exec(`CREATE TEMP TRIGGER fail_config_batch BEFORE UPDATE ON site_config WHEN NEW.key='donation_accept_enabled' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PatchSiteConfigBatch(context.Background(), input); err == nil {
		t.Fatal("injected failure accepted")
	}
	if value, _ := siteConfigRawValue(t, store, KeyCharityEnabled); value != "false" && value != "0" {
		t.Fatalf("partial write: %q", value)
	}
	if siteConfigRevision(t, store) != initial || siteConfigIdempotencyCount(t, store) != 0 {
		t.Fatal("failed batch changed revision or receipt")
	}
	if _, err := store.DB().Exec(`DROP TRIGGER fail_config_batch`); err != nil {
		t.Fatal(err)
	}
	first, err := repository.PatchSiteConfigBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if siteConfigRevision(t, store) != initial+1 {
		t.Fatal("batch must advance revision once")
	}
	replay, err := repository.PatchSiteConfigBatch(context.Background(), input)
	if err != nil || !replay.Replayed || string(replay.Body) != string(first.Body) {
		t.Fatalf("replay=%+v, error=%v", replay, err)
	}
	input.IdempotencyKey = "configuration-batch-0000002"
	if _, err := repository.PatchSiteConfigBatch(context.Background(), input); !errors.Is(err, ErrSiteConfigConflict) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	input.ExpectedRevision = strconv.FormatInt(initial+1, 10)
	unchanged, err := repository.PatchSiteConfigBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var response SiteConfigBatchResponse
	if err := json.Unmarshal(unchanged.Body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.ChangedKeys) != 0 || siteConfigRevision(t, store) != initial+1 {
		t.Fatal("unchanged values advanced revision")
	}
	authorizer.setError(ErrSiteConfigForbidden)
	if _, err := repository.PatchSiteConfigBatch(context.Background(), input); !errors.Is(err, ErrSiteConfigForbidden) {
		t.Fatalf("replay skipped authorization: %v", err)
	}
}

func TestConfigurationBatchRejectsDependenciesAndSpecializedSettings(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
	input := SiteConfigBatchInput{AdminID: 1, ExpectedRevision: strconv.FormatInt(siteConfigRevision(t, store), 10), IdempotencyKey: "configuration-batch-invalid1", Values: map[string]json.RawMessage{
		KeySiteName: json.RawMessage(`"Proposed name"`), KeyDonationAcceptEnabled: json.RawMessage(`true`),
	}}
	_, err := repository.PatchSiteConfigBatch(context.Background(), input)
	var validation *siteConfigValidationError
	if !errors.As(err, &validation) || validation.message != "Enable shared models before accepting donations." {
		t.Fatalf("missing dependency explanation: %v", err)
	}
	input.Values = map[string]json.RawMessage{"games_enabled": json.RawMessage(`true`)}
	if _, err := repository.PatchSiteConfigBatch(context.Background(), input); !errors.Is(err, ErrSiteConfigConflict) {
		t.Fatalf("specialized route bypass: %v", err)
	}
	if siteConfigIdempotencyCount(t, store) != 0 {
		t.Fatal("rejected batches left receipts")
	}
}
