package reports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// seedDonationKeyFixture inserts one terminal donation-key tombstone whose
// report fingerprint and source identity copy an existing physical endpoint
// key. It never writes secret or ciphertext material.
func (environment *reportTestEnvironment) seedDonationTombstone(
	t *testing.T,
	keyID int64,
	fingerprint [32]byte,
	endedReason string,
	endedAt int64,
	donorUserID *int64,
) int64 {
	t.Helper()
	return environment.seedDonationTombstoneSnapshot(
		t, keyID, fingerprint, endedReason, endedAt, donorUserID, "head", "tail",
	)
}

func (environment *reportTestEnvironment) seedDonationTombstoneSnapshot(
	t *testing.T,
	keyID int64,
	fingerprint [32]byte,
	endedReason string,
	endedAt int64,
	donorUserID *int64,
	displayHead string,
	displayTail string,
	expiresAt ...*int64,
) int64 {
	t.Helper()
	if len(expiresAt) > 1 {
		t.Fatal("seed tombstone accepts at most one expiry")
	}
	var expiry any
	if len(expiresAt) == 1 {
		expiry = expiresAt[0]
	}
	now := environment.clock.Load()
	var donationID int64
	result, err := environment.store.DB().Exec(`INSERT INTO donations(
user_id,status,revision,description,review_note,reviewed_by_user_id,reviewed_by_role,reviewed_at,created_at,updated_at,terminal_at)
VALUES(?, 'approved',1,'','',?,'admin',?,?,?,NULL)`,
		donorUserID, donorUserID, now, now, now)
	if err != nil {
		t.Fatalf("seed tombstone donation: %v", err)
	}
	donationID, err = result.LastInsertId()
	if err != nil {
		t.Fatalf("seed tombstone donation id: %v", err)
	}
	zero := make([]byte, 16)
	one := make([]byte, 16)
	one[15] = 1
	result, err = environment.store.DB().Exec(`INSERT INTO donation_keys(
 donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
 price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
 token_reserve,enabled,failure_disabled,failure_streak,streak_generation,next_claim_seq,next_fold_seq,
 safe_note,authorized_expires_at,expires_at,created_at,updated_at,ended_reason,ended_at,
 source_endpoint_key_id,report_fingerprint,report_match_until)
VALUES(?,NULL,?,?,?, ?,
 ?,?,?,?,?,?,
 0,0,0,?,?,?,?, '',?,?,
 ?, ?, ?, ?, ?, ?, ?)`,
		donationID, displayHead, displayTail, reportTestBaseURL, reportTestConnector,
		zero, zero, zero, zero, zero, zero,
		zero, one, one, zero,
		expiry, expiry,
		now-10, now-10, endedReason, endedAt, keyID, fingerprint[:], endedAt+90*24*60*60)
	if err != nil {
		t.Fatalf("seed tombstone donation key: %v", err)
	}
	donationKeyID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed tombstone donation key id: %v", err)
	}
	return donationKeyID
}

func (environment *reportTestEnvironment) seedTerminalDonationTombstone(
	t *testing.T,
	keyID int64,
	fingerprint [32]byte,
	terminalAt int64,
	donorUserID *int64,
) int64 {
	t.Helper()
	previousNow := environment.clock.Load()
	environment.setNow(terminalAt)
	donationKeyID := environment.seedDonationTombstone(
		t, keyID, fingerprint, "expired", terminalAt, donorUserID,
	)
	if _, err := environment.store.DB().Exec(`UPDATE donations SET
status='expired',terminal_at=?,updated_at=?
WHERE id=(SELECT donation_id FROM donation_keys WHERE id=?)`, terminalAt, terminalAt, donationKeyID); err != nil {
		t.Fatalf("terminalize donation root: %v", err)
	}
	environment.setNow(previousNow)
	return donationKeyID
}

func (environment *reportTestEnvironment) tombstoneTargetState(t *testing.T, caseID string) (int64, string, int64) {
	t.Helper()
	var sourceKeyID int64
	var state string
	var endpointKeyID sql.NullInt64
	if err := environment.store.DB().QueryRow(`SELECT source_endpoint_key_id,state,endpoint_key_id
FROM report_targets WHERE case_id=? AND state='released' AND endpoint_key_id IS NULL`, caseID).Scan(
		&sourceKeyID, &state, &endpointKeyID); err != nil {
		t.Fatalf("read tombstone target: %v", err)
	}
	return sourceKeyID, state, endpointKeyID.Int64
}

func fingerprintForSecret(t *testing.T, environment *reportTestEnvironment, secretValue string) [32]byte {
	t.Helper()
	fingerprint, err := environment.repository.keys.fingerprintDigest(reportTestConnector, reportTestBaseURL, []byte(secretValue))
	if err != nil {
		t.Fatalf("fingerprint secret: %v", err)
	}
	return fingerprint
}

// seedActiveCase inserts a pending_indexing case plus its material and
// indexing operation directly. It is retained for worker-state tests that
// need to start at a precise cursor without exercising public acceptance.
func (environment *reportTestEnvironment) seedActiveCase(t *testing.T, fingerprint [32]byte) string {
	t.Helper()
	now := environment.clock.Load()
	caseID := reportTestOpaqueID("rpc_", environment.ids.Add(1))
	materialHash := fingerprint
	envelope := make([]byte, 45)
	if _, err := environment.store.DB().Exec(`INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_source,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,legal_hold_consumed)
VALUES(?,?,?,?,'pending_indexing','in_progress',1,1,?,NULL,NULL,1,0,0,0,0,0,0,?,0)`,
		caseID, fingerprint[:], reportTestConnector, reportTestBaseURL,
		now+86400, now); err != nil {
		t.Fatalf("seed case: %v", err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO report_materials(
 case_id,material_hash,note_text,source_ip_envelope,created_at)
VALUES(?,?,?,?,?)`, caseID, materialHash[:], "seeded", envelope, now); err != nil {
		t.Fatalf("seed case material: %v", err)
	}
	operationID := reportTestOpaqueID("op_", environment.ids.Add(1))
	if _, err := environment.store.DB().Exec(`INSERT INTO accepted_operations(
 id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at)
VALUES(?,'report_indexing',NULL,'',?,'accepted',?,NULL,?,NULL)`,
		operationID, fingerprint[:], caseID, now); err != nil {
		t.Fatalf("seed indexing operation: %v", err)
	}
	return caseID
}

func TestReportTombstoneIndexingDedupAndLivePriority(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	keyID := environment.seedEndpointKey(t, endpointID, "tombstone-secret", reportTestNow)
	fingerprint := fingerprintForSecret(t, environment, "tombstone-secret")

	// Live-only first: an active case indexes the physical key as protected.
	environment.accept(t, "tombstone-secret", "live material", 3000, 1, nil)
	liveCaseID := environment.caseIDForSecret(t, "tombstone-secret")
	environment.runUntilIdle(t, 4)
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='protected' AND endpoint_key_id IS NOT NULL`, liveCaseID); got != 1 {
		t.Fatalf("live target count=%d", got)
	}

	// Terminate the live case, then delete the physical key so the seeded
	// tombstone becomes the only matchable trace.
	admin := environment.seedActor(t, true, 1)
	_, _, materialVersion, targetVersion := environment.caseState(t, liveCaseID)
	if _, err := environment.repository.Reject(context.Background(), admin, liveCaseID, RejectCommand{
		ExpectedMaterialVersion: materialVersion, ExpectedTargetVersion: targetVersion,
		Reason: "live case closed", IdempotencyKey: reportTestKey(3001),
	}); err != nil {
		t.Fatalf("reject live case: %v", err)
	}
	if _, err := environment.store.DB().Exec(`DELETE FROM endpoint_keys WHERE id=?`, keyID); err != nil {
		t.Fatalf("delete endpoint key: %v", err)
	}
	environment.seedDonationTombstone(t, keyID, fingerprint, "member_removed", environment.clock.Load()-10, &owner.UserID)

	// A fresh public HTTP submission must reach the tombstone even though no
	// live key exists, while preserving the opaque accepted wire.
	environment.setNow(environment.clock.Load() + 1)
	response := invokeAnonymousReport(environment.repository,
		reportPublicBody(reportTestConnector, reportTestBaseURL, "tombstone-secret", nil),
		reportTestKey(3002), "application/json", "")
	assertAcceptedWire(t, response)
	tombstoneCaseID := environment.caseIDForSecret(t, "tombstone-secret")
	if tombstoneCaseID == liveCaseID {
		t.Fatal("expected a fresh case for the closed prior one")
	}
	environment.runUntilIdle(t, 4)
	sourceKeyID, state, physical := environment.tombstoneTargetState(t, tombstoneCaseID)
	if sourceKeyID != keyID || state != "released" || physical != 0 {
		t.Fatalf("tombstone target source=%d state=%s physical=%d", sourceKeyID, state, physical)
	}

	// Re-running the worker must never duplicate the tombstone target.
	before := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, tombstoneCaseID)
	environment.runUntilIdle(t, 4)
	if after := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, tombstoneCaseID); after != before {
		t.Fatalf("tombstone duplicated: before=%d after=%d", before, after)
	}

}

func TestReportLiveTargetDeduplicatesSameSourceTombstone(t *testing.T) {
	environment := newReportTestEnvironment(t)
	now := reportTestNow
	environment.setNow(now)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	keyID := environment.seedEndpointKey(t, endpointID, "dedup-secret", reportTestNow)
	fingerprint := fingerprintForSecret(t, environment, "dedup-secret")
	// A per-key expiry terminalizes the donation key while the physical key
	// stays alive for self-use; both scans therefore see one source identity.
	environment.seedDonationTombstone(t, keyID, fingerprint, "expired", now-10, &owner.UserID)
	_ = endpointID

	caseID := environment.seedActiveCase(t, fingerprint)
	environment.runUntilIdle(t, 4)
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, caseID); got != 1 {
		t.Fatalf("target total=%d, tombstone must dedup against live", got)
	}
	var state string
	var endpointKeyID sql.NullInt64
	if err := environment.store.DB().QueryRow(`SELECT state,endpoint_key_id FROM report_targets WHERE case_id=?`, caseID).Scan(&state, &endpointKeyID); err != nil {
		t.Fatal(err)
	}
	if state != "protected" || !endpointKeyID.Valid || endpointKeyID.Int64 != keyID {
		t.Fatalf("live target state=%s endpoint=%v", state, endpointKeyID)
	}
}

func TestReportTombstoneMatchWindowAndFingerprintCleanup(t *testing.T) {
	environment := newReportTestEnvironment(t)
	now := reportTestNow
	environment.setNow(now)

	tests := []struct {
		secret        string
		sourceKeyID   int64
		deadlineDelta int64
		wantMatch     bool
	}{
		{secret: "window-before", sourceKeyID: 990001, deadlineDelta: 1, wantMatch: true},
		{secret: "window-exact", sourceKeyID: 990002, deadlineDelta: 0, wantMatch: false},
		{secret: "window-after", sourceKeyID: 990003, deadlineDelta: -1, wantMatch: false},
	}
	for index, test := range tests {
		fingerprint := fingerprintForSecret(t, environment, test.secret)
		endedAt := now - caseRetentionSeconds + test.deadlineDelta
		environment.seedDonationTombstone(t, test.sourceKeyID, fingerprint, "terminated", endedAt, nil)
		response := invokeAnonymousReport(environment.repository,
			reportPublicBody(reportTestConnector, reportTestBaseURL, test.secret, nil),
			reportTestKey(3100+index), "application/json", "")
		assertAcceptedWire(t, response)
		got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases WHERE fingerprint=?`, fingerprint[:])
		if (got == 1) != test.wantMatch {
			t.Fatalf("%s case count=%d wantMatch=%v", test.secret, got, test.wantMatch)
		}
	}

	environment.runUntilIdle(t, 4)
	caseID := environment.caseIDForSecret(t, "window-before")
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, caseID); got != 1 {
		t.Fatalf("before-deadline target count=%d", got)
	}
	sourceKeyID, _, _ := environment.tombstoneTargetState(t, caseID)
	if sourceKeyID != 990001 {
		t.Fatalf("before-deadline tombstone source=%d", sourceKeyID)
	}

	// At the exact decision second, cleanup clears the exact and elapsed
	// fingerprints, leaves the future one intact, and never moves any derived
	// match deadline.
	if _, err := environment.repository.RetainLifecycle(
		context.Background(), now, 100, time.Now().Add(time.Second),
	); err != nil {
		t.Fatalf("RetainLifecycle: %v", err)
	}
	for _, test := range tests {
		var storedFingerprint []byte
		var matchUntil sql.NullInt64
		if err := environment.store.DB().QueryRow(`SELECT report_fingerprint,report_match_until
FROM donation_keys WHERE source_endpoint_key_id=?`, test.sourceKeyID).Scan(&storedFingerprint, &matchUntil); err != nil {
			t.Fatal(err)
		}
		wantFingerprint := test.deadlineDelta > 0
		if (len(storedFingerprint) == 32) != wantFingerprint {
			t.Fatalf("%s fingerprint length=%d want retained=%v", test.secret, len(storedFingerprint), wantFingerprint)
		}
		if !matchUntil.Valid || matchUntil.Int64 != now+test.deadlineDelta {
			t.Fatalf("%s match deadline=%v want=%d", test.secret, matchUntil, now+test.deadlineDelta)
		}
	}
}

func TestReportTargetDonationLineageReadsAndBoundaries(t *testing.T) {
	environment := newReportTestEnvironment(t)
	now := reportTestNow
	environment.setNow(now)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	keyID := environment.seedEndpointKey(t, endpointID, "lineage-secret", reportTestNow)
	fingerprint := fingerprintForSecret(t, environment, "lineage-secret")

	// Three donation keys share the same physical source identity across two
	// donations; one carries a finite effective expiry.
	firstKey := environment.seedDonationTombstone(t, keyID, fingerprint, "member_removed", now-50, &owner.UserID)
	effectiveExpiry := now + 3600
	secondKey := environment.seedDonationTombstoneSnapshot(
		t, keyID, fingerprint, "terminated", now-40, &owner.UserID, "head", "tail", &effectiveExpiry,
	)
	thirdKey := environment.seedDonationTombstone(t, keyID, fingerprint, "terminated", now-30, &owner.UserID)
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET safe_note='private-safe-note-canary' WHERE id=?`, firstKey); err != nil {
		t.Fatalf("set private safe note: %v", err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE donations SET description='private-donor-description-canary'
WHERE id=(SELECT donation_id FROM donation_keys WHERE id=?)`, firstKey); err != nil {
		t.Fatalf("set private donor description: %v", err)
	}
	// Ordinary terminal-donation retention is exact: the row one second
	// before its 400-day deadline remains visible, while exact-deadline and
	// one-second-past rows are excluded from both count and lineage.
	visibleBoundaryKey := environment.seedTerminalDonationTombstone(
		t, keyID, fingerprint, now-donationRetentionSeconds+1, &owner.UserID,
	)
	environment.seedTerminalDonationTombstone(
		t, keyID, fingerprint, now-donationRetentionSeconds, &owner.UserID,
	)
	environment.seedTerminalDonationTombstone(
		t, keyID, fingerprint, now-donationRetentionSeconds-1, &owner.UserID,
	)

	environment.accept(t, "lineage-secret", "lineage material", 3200, 1, nil)
	caseID := environment.caseIDForSecret(t, "lineage-secret")
	environment.runUntilIdle(t, 4)
	admin := environment.seedActor(t, true, 1)

	targets, err := environment.repository.Targets(context.Background(), admin, caseID, "", 10)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets.Data) != 1 {
		t.Fatalf("target page size=%d", len(targets.Data))
	}
	target := targets.Data[0]
	if target.DonationMatchCount != "4" {
		t.Fatalf("donation_match_count=%s", target.DonationMatchCount)
	}

	// Full page read.
	page, err := environment.repository.TargetDonations(context.Background(), admin, caseID, target.ID, "", 50)
	if err != nil {
		t.Fatalf("TargetDonations: %v", err)
	}
	if len(page.Data) != 4 || page.NextCursor != nil {
		t.Fatalf("lineage page=%d next=%v", len(page.Data), page.NextCursor)
	}
	wire, err := json.Marshal(page.Data[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"private-safe-note-canary", "private-donor-description-canary"} {
		if strings.Contains(string(wire), canary) {
			t.Fatalf("lineage leaked %q: %s", canary, wire)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	allowedFields := map[string]bool{
		"donation_id": true, "donation_key_id": true, "donation_status": true,
		"key_state": true, "expires_at": true, "ended_reason": true, "ended_at": true,
	}
	if len(fields) != len(allowedFields) {
		t.Fatalf("lineage wire fields=%v", fields)
	}
	for field := range fields {
		if !allowedFields[field] {
			t.Fatalf("unsafe lineage wire field=%q", field)
		}
	}
	if page.Data[0].DonationKeyID >= page.Data[1].DonationKeyID ||
		page.Data[1].DonationKeyID >= page.Data[2].DonationKeyID ||
		page.Data[2].DonationKeyID >= page.Data[3].DonationKeyID {
		t.Fatalf("lineage not ordered by donation key id: %+v", page.Data)
	}
	byKey := map[string]ReportDonationMatch{}
	for _, item := range page.Data {
		byKey[item.DonationKeyID] = item
	}
	first := byKey[int64ToString(firstKey)]
	second := byKey[int64ToString(secondKey)]
	third := byKey[int64ToString(thirdKey)]
	if first.KeyState != "ended" || first.EndedReason == nil || *first.EndedReason != "member_removed" || first.EndedAt == nil {
		t.Fatalf("first lineage item=%+v", first)
	}
	if second.ExpiresAt == nil || *second.ExpiresAt != now+3600 {
		t.Fatalf("second lineage item=%+v", second)
	}
	if third.KeyState != "ended" {
		t.Fatalf("third lineage item=%+v", third)
	}

	// Cursor pagination must split deterministically and terminate.
	firstPage, err := environment.repository.TargetDonations(context.Background(), admin, caseID, target.ID, "", 2)
	if err != nil {
		t.Fatalf("TargetDonations first page: %v", err)
	}
	if len(firstPage.Data) != 2 || firstPage.NextCursor == nil {
		t.Fatalf("first lineage page=%d next=%v", len(firstPage.Data), firstPage.NextCursor)
	}
	secondPage, err := environment.repository.TargetDonations(
		context.Background(), admin, caseID, target.ID, *firstPage.NextCursor, 2)
	if err != nil {
		t.Fatalf("TargetDonations second page: %v", err)
	}
	if len(secondPage.Data) != 2 || secondPage.NextCursor != nil {
		t.Fatalf("second lineage page=%d next=%v", len(secondPage.Data), secondPage.NextCursor)
	}
	if secondPage.Data[0].DonationKeyID != int64ToString(thirdKey) {
		t.Fatalf("second lineage page item=%+v", secondPage.Data[0])
	}
	if secondPage.Data[1].DonationKeyID != int64ToString(visibleBoundaryKey) {
		t.Fatalf("400-day boundary lineage item=%+v", secondPage.Data[1])
	}

	// Cross case/target ownership is rejected with NotFound.
	stranger := environment.seedActor(t, false, 1)
	if _, err := environment.repository.TargetDonations(context.Background(), stranger, caseID, target.ID, "", 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin lineage read err=%v", err)
	}
	otherCase := environment.caseIDForSecret(t, "lineage-secret")
	otherCaseID := otherCase[:len(otherCase)-1] + "A"
	if otherCaseID == caseID {
		otherCaseID = otherCase[:len(otherCase)-1] + "Q"
	}
	if _, err := environment.repository.TargetDonations(context.Background(), admin, otherCaseID, target.ID, "", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-case lineage read err=%v", err)
	}
	// A syntactically valid but unknown target id is not found.
	if _, err := environment.repository.TargetDonations(context.Background(), admin, caseID, reportTestOpaqueID("rpt_", 987654321), "", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign target lineage read err=%v", err)
	}
	// A malformed target id is rejected before any lookup.
	if _, err := environment.repository.TargetDonations(context.Background(), admin, caseID, "rpt_not-an-oid", "", 10); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("malformed target lineage read err=%v", err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE sessions SET cred_gen='replaced-generation'
WHERE token_hash=?`, admin.SessionTokenHash); err != nil {
		t.Fatalf("replace admin session generation: %v", err)
	}
	if _, err := environment.repository.TargetDonations(context.Background(), admin, caseID, target.ID, "", 10); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale-generation lineage read err=%v", err)
	}
}

func TestReportApprovalTombstoneTargetIsSafeNoOp(t *testing.T) {
	environment := newReportTestEnvironment(t)
	now := reportTestNow
	environment.setNow(now)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	keyID := environment.seedEndpointKey(t, endpointID, "noop-secret", reportTestNow)
	fingerprint := fingerprintForSecret(t, environment, "noop-secret")

	// Capture a live protected target first. Its physical key then disappears
	// before adjudication, while donation lineage keeps the immutable source id.
	environment.accept(t, "noop-secret", "missing physical key", 3300, 1, nil)
	caseID := environment.caseIDForSecret(t, "noop-secret")
	environment.runUntilIdle(t, 4)
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='protected' AND endpoint_key_id=?`, caseID, keyID); got != 1 {
		t.Fatalf("protected target count=%d", got)
	}
	environment.seedDonationTombstone(t, keyID, fingerprint, "member_removed", now-10, &owner.UserID)
	if _, err := environment.store.DB().Exec(`DELETE FROM endpoint_keys WHERE id=?`, keyID); err != nil {
		t.Fatalf("delete endpoint key: %v", err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='protected' AND endpoint_key_id IS NULL`, caseID); got != 1 {
		t.Fatalf("missing physical protected target count=%d", got)
	}
	_, _, materialVersion, targetVersion := environment.caseState(t, caseID)
	admin := environment.seedActor(t, true, 1)
	result, err := environment.repository.Approve(context.Background(), admin, caseID, ApproveCommand{
		ExpectedMaterialVersion: materialVersion, ExpectedTargetVersion: targetVersion,
		Reason: "tombstone approval", Confirmation: true, IdempotencyKey: reportTestKey(3301),
	})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if result.Status != http.StatusAccepted {
		t.Fatalf("approval status=%d", result.Status)
	}
	environment.runUntilIdle(t, 4)
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "approved" || progress != "complete" {
		t.Fatalf("approval state=%s/%s", status, progress)
	}
	if environment.deletions.callCount() != 0 {
		t.Fatalf("missing-key approval attempted %d physical deletions", environment.deletions.callCount())
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=? AND state='released'`, caseID); got != 1 {
		t.Fatalf("missing-key target was not released: %d", got)
	}
	var targetID string
	if err := environment.store.DB().QueryRow(`SELECT id FROM report_targets WHERE case_id=?`, caseID).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	lineage, err := environment.repository.TargetDonations(context.Background(), admin, caseID, targetID, "", 10)
	if err != nil {
		t.Fatalf("TargetDonations after no-op approval: %v", err)
	}
	if len(lineage.Data) != 1 || lineage.Data[0].DonationKeyID == "" || lineage.Data[0].KeyState != "ended" {
		t.Fatalf("lineage after no-op approval=%+v", lineage)
	}
	var targetCount, processedCount, deletedCount, releasedCount int64
	if err := environment.store.DB().QueryRow(`SELECT target_count,processed_target_count,
deleted_target_count,released_target_count FROM report_cases WHERE id=?`, caseID).Scan(
		&targetCount, &processedCount, &deletedCount, &releasedCount,
	); err != nil {
		t.Fatal(err)
	}
	if targetCount != 1 || processedCount != 1 || deletedCount != 0 || releasedCount != 1 {
		t.Fatalf("approval counters target=%d processed=%d deleted=%d released=%d",
			targetCount, processedCount, deletedCount, releasedCount)
	}
}

func TestReportAccountDeletionKeepsTombstoneTraceWithoutIdentity(t *testing.T) {
	environment := newReportTestEnvironment(t)
	now := reportTestNow
	environment.setNow(now)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	keyID := environment.seedEndpointKey(t, endpointID, "deletion-secret", reportTestNow)
	fingerprint := fingerprintForSecret(t, environment, "deletion-secret")
	environment.seedDonationTombstone(t, keyID, fingerprint, "account_deleted", now-10, &owner.UserID)
	if _, err := environment.store.DB().Exec(`DELETE FROM endpoint_keys WHERE id=?`, keyID); err != nil {
		t.Fatalf("delete endpoint key: %v", err)
	}

	caseID := environment.seedActiveCase(t, fingerprint)
	environment.runUntilIdle(t, 4)
	sourceKeyID, state, physical := environment.tombstoneTargetState(t, caseID)
	if sourceKeyID != keyID || state != "released" || physical != 0 {
		t.Fatalf("post-deletion tombstone source=%d state=%s physical=%d", sourceKeyID, state, physical)
	}
	var ownerID sql.NullInt64
	if err := environment.store.DB().QueryRow(`SELECT owner_user_id FROM report_targets
WHERE case_id=? AND endpoint_key_id IS NULL`, caseID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if !ownerID.Valid || ownerID.Int64 != owner.UserID {
		t.Fatalf("tombstone owner=%v", ownerID)
	}

	// Deleting the account scrubs target identity but the terminal tombstone
	// target stays adjudicable for the case's own lifecycle.
	if _, err := environment.store.DB().Exec(`DELETE FROM users WHERE id=?`, owner.UserID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if err := environment.store.DB().QueryRow(`SELECT owner_user_id FROM report_targets
WHERE case_id=? AND endpoint_key_id IS NULL`, caseID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if ownerID.Valid {
		t.Fatalf("tombstone owner survived account deletion: %v", ownerID)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, caseID); got != 1 {
		t.Fatalf("tombstone target count after deletion=%d", got)
	}
}

func TestReportTombstoneIndexingUsesLatestSafeSnapshot(t *testing.T) {
	environment := newReportTestEnvironment(t)
	environment.setNow(reportTestNow)
	fingerprint := fingerprintForSecret(t, environment, "latest-snapshot")
	const sourceKeyID int64 = 770001
	environment.seedDonationTombstoneSnapshot(
		t, sourceKeyID, fingerprint, "terminated", reportTestNow-20, nil, "older", "old",
	)
	environment.seedDonationTombstoneSnapshot(
		t, sourceKeyID, fingerprint, "terminated", reportTestNow-10, nil, "tie-lower-id", "lose",
	)
	winnerID := environment.seedDonationTombstoneSnapshot(
		t, sourceKeyID, fingerprint, "terminated", reportTestNow-10, nil, "tie-higher-id", "win",
	)

	response := invokeAnonymousReport(environment.repository,
		reportPublicBody(reportTestConnector, reportTestBaseURL, "latest-snapshot", nil),
		reportTestKey(3400), "application/json", "")
	assertAcceptedWire(t, response)
	caseID := environment.caseIDForSecret(t, "latest-snapshot")
	environment.runUntilIdle(t, 4)

	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, caseID); got != 1 {
		t.Fatalf("deduplicated target count=%d", got)
	}
	var displayHead, displayTail string
	if err := environment.store.DB().QueryRow(`SELECT key_display_head,key_display_tail
FROM report_targets WHERE case_id=? AND source_endpoint_key_id=?`, caseID, sourceKeyID).Scan(
		&displayHead, &displayTail,
	); err != nil {
		t.Fatal(err)
	}
	if displayHead != "tie-higher-id" || displayTail != "win" {
		t.Fatalf("latest safe snapshot=%q/%q winner donation key=%d", displayHead, displayTail, winnerID)
	}
}

func TestReportWorkerRestoreFindsLiveTargetsAfterRestart(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	fingerprint := fingerprintForSecret(t, environment, "restore-secret")
	// This donation-key id is deliberately lower than every live endpoint-key
	// cursor. Reusing the endpoint cursor in the donation id domain would lose
	// it after the phase switch.
	environment.seedDonationTombstone(t, 880001, fingerprint, "member_removed", reportTestNow-10, &owner.UserID)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	for index := 0; index < workerBatchLimit+2; index++ {
		environment.seedEndpointKey(t, endpointID, "restore-secret", reportTestNow)
	}
	environment.accept(t, "restore-secret", "restore material", 3500, 1, nil)
	caseID := environment.caseIDForSecret(t, "restore-secret")

	// First bounded pass leaves the case mid-indexing with an endpoint cursor.
	first, err := environment.repository.RunWorkerOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.More {
		t.Fatalf("first pass result=%+v", first)
	}
	var cursorSource *string
	var cursorID *int64
	if err := environment.store.DB().QueryRow(`SELECT cursor_source,cursor_id FROM report_cases WHERE id=?`, caseID).Scan(&cursorSource, &cursorID); err != nil {
		t.Fatal(err)
	}
	if cursorSource == nil || *cursorSource != indexPhaseEndpoint || cursorID == nil || *cursorID <= 0 {
		t.Fatalf("endpoint cursor=%v/%v", cursorSource, cursorID)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, caseID); got != workerBatchLimit {
		t.Fatalf("first pass target count=%d", got)
	}

	// The next pass consumes the final live keys and commits the donation/0
	// phase marker without scanning donation rows in the same transaction.
	second, err := environment.repository.RunWorkerOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !second.More {
		t.Fatalf("phase-switch pass result=%+v", second)
	}
	if err := environment.store.DB().QueryRow(`SELECT cursor_source,cursor_id FROM report_cases WHERE id=?`, caseID).Scan(&cursorSource, &cursorID); err != nil {
		t.Fatal(err)
	}
	if cursorSource == nil || *cursorSource != indexPhaseDonation || cursorID == nil || *cursorID != 0 {
		t.Fatalf("committed phase marker=%v/%v", cursorSource, cursorID)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets WHERE case_id=?`, caseID); got != workerBatchLimit+2 {
		t.Fatalf("phase-switch target count=%d", got)
	}
	var operationState string
	if err := environment.store.DB().QueryRow(`SELECT state FROM accepted_operations
WHERE kind='report_indexing' AND checkpoint=?`, caseID).Scan(&operationState); err != nil {
		t.Fatal(err)
	}
	if operationState != "running" {
		t.Fatalf("crash-left operation state=%s", operationState)
	}

	// Startup recovery requeues the crash-left operation, resumes from the
	// committed donation/0 marker, captures the low-id tombstone, and completes.
	recovered, err := environment.repository.RecoverBeforeListener(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.CasesProcessed != 1 {
		t.Fatalf("recovery result=%+v", recovered)
	}
	status, progress, _, _ := environment.caseState(t, caseID)
	if status != "pending_review" || progress != "complete" {
		t.Fatalf("recovered state=%s/%s", status, progress)
	}
	if got := environment.rowCount(t, `SELECT target_count FROM report_cases WHERE id=?`, caseID); got != workerBatchLimit+3 {
		t.Fatalf("recovered target count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND source_endpoint_key_id=880001 AND state='released' AND endpoint_key_id IS NULL`, caseID); got != 1 {
		t.Fatalf("recovered low-id tombstone count=%d", got)
	}
	var finalSource *string
	var finalID *int64
	if err := environment.store.DB().QueryRow(`SELECT cursor_source,cursor_id FROM report_cases WHERE id=?`, caseID).Scan(&finalSource, &finalID); err != nil {
		t.Fatal(err)
	}
	if finalSource != nil || finalID != nil {
		t.Fatalf("terminal cursor=%v/%v", finalSource, finalID)
	}
}

func one128() []byte {
	value := make([]byte, 16)
	value[15] = 1
	return value
}

func zero128() []byte {
	return make([]byte, 16)
}

func int64ToString(value int64) string {
	if value < 0 {
		return ""
	}
	digits := []byte{}
	if value == 0 {
		digits = append(digits, '0')
	}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
