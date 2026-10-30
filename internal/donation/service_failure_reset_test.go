package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func seedResetHealth(t *testing.T, e *donationTestEnv, keyID int64) (int64, int64) {
	t.Helper()
	_, err := e.store.DB().Exec(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,failure_disabled,next_claim_seq,next_fold_seq,retry_after_until,backoff_generation,backoff_episode,backoff_period,backoff_source,backoff_stage,backoff_retry_at,updated_at)
 SELECT u.id,dk.endpoint_key_id,u.native_service_bit,g.credential_version_id,nbi_u128(7),nbi_u128(CASE WHEN u.native_service_bit=1 THEN 10 ELSE 0 END),u.native_service_bit=1,nbi_u128(4),nbi_u128(2),?,3,2,'day','builtin',1,?,?
 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE dk.id=?`, donationTestNow+60, donationTestNow+120, donationTestNow, keyID)
	if err != nil {
		t.Fatal(err)
	}
	var id, revision int64
	if err = e.store.DB().QueryRow(`SELECT id,revision FROM donation_uses WHERE donation_key_id=? AND native_service_bit=1`, keyID).Scan(&id, &revision); err != nil {
		t.Fatal(err)
	}
	return id, revision
}

func TestServiceFailureResetAllEntryPoints(t *testing.T) {
	for _, entry := range []string{"owner", "batch", "editor", "owner_use", "admin_use", "agent_key", "agent_use"} {
		t.Run(entry, func(t *testing.T) {
			e := newDonationTestEnv(t)
			admin := e.seedUser(t, "", nil, true)
			owner := e.seedUser(t, "service-reset-owner", nil, false)
			_, key := e.seedEndpointKey(t, owner, 's')
			d := approvedResetDonation(t, e, owner, key)
			donationID, keyID := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
			useID, useRevision := seedResetHealth(t, e, keyID)
			if _, err := e.store.DB().Exec(`UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE donation_key_id=? AND native_service_bit<>1`, keyID); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			switch entry {
			case "owner":
				value, err := e.service.resetFailureOwner(ctx, owner, donationID, keyID, 2, donationMutation(t, 'X', http.MethodPost, routeOwnerFailureReset, []int64{donationID, keyID}, map[string]string{"expected_revision": "2"}))
				if err != nil || value.Value.Outcome != "recovered" {
					t.Fatalf("%+v %v", value, err)
				}
			case "batch":
				value, err := e.service.resetFailureBatch(ctx, reviewerAdmin, 0, []failureResetItem{{donationID, keyID, 2}}, donationMutation(t, 'X', http.MethodPost, routeAdminFailureReset, nil, map[string]any{"items": []FailureResetRef{failureResetRef(donationID, keyID, 2)}}))
				if err != nil || value.Value.Counts.Recovered != "1" {
					t.Fatalf("%+v %v", value, err)
				}
				if len(value.Value.Results[0].Services) < 2 {
					t.Fatal("fixture must exercise multiple services")
				}
			case "editor":
				input := KeyManagementInput{ExpectedRevision: 2, ResetFailureStreak: true}
				_, err := e.service.ManageKeyAdmin(ctx, donationID, keyID, donationMutation(t, 'X', http.MethodPatch, routeAdminKey, []int64{donationID, keyID}, map[string]any{"expected_revision": "2", "reset_failure_streak": true}), input)
				if err != nil {
					t.Fatal(err)
				}
			case "owner_use", "admin_use":
				role, actor := recurringOwner, owner
				if entry == "admin_use" {
					role, actor = reviewerAdmin, 0
				}
				input := UsePatch{ExpectedRevision: useRevision}
				value, err := e.service.mutateUse(ctx, role, actor, donationID, useID, input, "reset", donationMutation(t, 'X', http.MethodPost, useRoute(role)+"/{useId}/failure-streak-reset", []int64{donationID, useID}, map[string]string{"expected_revision": fmt.Sprint(useRevision)}))
				if err != nil || value.Value.FailureReset == nil || value.Value.FailureReset.Status != "reset" {
					t.Fatalf("%+v %v", value, err)
				}
			default:
				tx, err := e.store.DB().BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				kind, ref, expected := "donation.key.reset", keyID, int64(2)
				if entry == "agent_use" {
					kind, ref, expected = "donation.use.reset", useID, useRevision
				}
				input := fmt.Sprintf(`{"expected_revision":"%d","key_id":"%d"}`, expected, ref)
				if entry == "agent_use" {
					input = fmt.Sprintf(`{"expected_revision":"%d","use_id":"%d"}`, expected, ref)
				}
				_, err = e.service.AgentApply(ctx, tx, agentplan.Actor{ID: admin, Role: "admin"}, agentplan.Operation{Kind: kind, TargetID: d.ID, Input: json.RawMessage(input)}, allowAgent)
				if err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := e.store.DB().Query(`SELECT native_service_bit,hex(streak_generation),hex(failure_streak),failure_disabled,retry_after_until,backoff_retry_at,backoff_generation,backoff_episode FROM donation_service_health ORDER BY id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var bit, retry, backoff, generation, episode int64
				var streakGen, streak string
				var disabled bool
				if err := rows.Scan(&bit, &streakGen, &streak, &disabled, &retry, &backoff, &generation, &episode); err != nil {
					t.Fatal(err)
				}
				expected := "00000000000000000000000000000008"
				if strings.HasSuffix(entry, "_use") && bit != 1 {
					expected = "00000000000000000000000000000007"
				}
				if streakGen != expected || streak != strings.Repeat("0", 32) || disabled || retry != donationTestNow+60 || backoff != donationTestNow+120 || generation != 3 || episode != 2 {
					t.Fatalf("health changed incorrectly: %d %s %s %t %d %d %d %d", bit, streakGen, streak, disabled, retry, backoff, generation, episode)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceFailureResetSkipsInvalidApprovalAndCredential(t *testing.T) {
	for _, invalid := range []string{"review_required", "credential_unavailable"} {
		t.Run(invalid, func(t *testing.T) {
			e := newDonationTestEnv(t)
			e.seedUser(t, "", nil, true)
			owner := e.seedUser(t, "invalid-reset-owner", nil, false)
			_, physical := e.seedEndpointKey(t, owner, 'v')
			d := approvedResetDonation(t, e, owner, physical)
			id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
			useID, revision := seedResetHealth(t, e, key)
			var err error
			if invalid == "review_required" {
				_, err = e.store.DB().Exec(`INSERT INTO donation_key_review_requirements(hmac,required,revision,created_at,updated_at) SELECT DISTINCT key_body_review_hmac,1,1,?,? FROM donation_use_revisions WHERE use_id IN(SELECT id FROM donation_uses WHERE donation_key_id=?) ON CONFLICT(hmac) DO UPDATE SET required=1,revision=revision+1`, donationTestNow, donationTestNow, key)
			} else {
				_, err = e.store.DB().Exec(`UPDATE credential_versions SET enabled=0 WHERE id IN(SELECT g.credential_version_id FROM donation_uses u JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE u.donation_key_id=?)`, key)
			}
			if err != nil {
				t.Fatal(err)
			}
			out, err := e.service.resetFailureBatch(context.Background(), reviewerAdmin, 0, []failureResetItem{{id, key, 2}}, donationMutation(t, 'N', http.MethodPost, routeAdminFailureReset, nil, map[string]any{"items": []FailureResetRef{failureResetRef(id, key, 2)}}))
			if err != nil || out.Value.Results[0].Status != "no_service" || out.Value.Counts.Reset != "0" {
				t.Fatalf("%+v %v", out, err)
			}
			for _, s := range out.Value.Results[0].Services {
				if s.Reason != invalid {
					t.Fatalf("%+v", s)
				}
			}
			tx, err := e.store.DB().Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err = agentResetUseTx(context.Background(), tx, useID, revision, donationTestNow); !errors.Is(err, ErrNoResetService) || !errors.Is(err, ErrConflict) {
				t.Fatalf("single reset: %v", err)
			}
			tx.Rollback()
			var streak []byte
			if err = e.store.DB().QueryRow("SELECT failure_streak FROM donation_service_health WHERE use_id=?", useID).Scan(&streak); err != nil || testU128Decimal(t, streak) != "10" {
				t.Fatal(streak, err)
			}
		})
	}
}

func TestServiceFailureResetAuditCapturesRealChangesAndZeroGeneration(t *testing.T) {
	e := newDonationTestEnv(t)
	admin := e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "audit-reset-owner", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'l')
	d := approvedResetDonation(t, e, owner, physical)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	seedResetHealth(t, e, key)
	var installed bool
	if err := e.store.DB().QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='management_capture_donation_service_health_update')").Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if !installed {
		if _, err := e.store.DB().Exec(db.ManagementServiceHealthSQL); err != nil {
			t.Fatal(err)
		}
	}
	e.store.SetManagementCapture(managementlog.CaptureEventID)
	for round := 0; round < 2; round++ {
		ctx := managementlog.WithOperation(context.Background(), managementlog.Metadata{Source: managementlog.SourceWeb, ExecutorKind: "human", Actor: managementlog.Subject{ID: fmt.Sprint(admin), Name: "Administrator", Role: "admin"}, Domain: "donations", Action: "failure_streak.reset"})
		out, err := e.service.resetFailureBatch(ctx, reviewerAdmin, 0, []failureResetItem{{id, key, int64(2 + round)}}, donationMutation(t, byte('G'+round), http.MethodPost, routeAdminFailureReset, nil, map[string]any{"items": []FailureResetRef{failureResetRef(id, key, int64(2+round))}}))
		if err != nil {
			t.Fatal(err)
		}
		expected := "recovered"
		if round == 1 {
			expected = "already_clear"
		}
		if out.Value.Results[0].Outcome != expected {
			t.Fatal(out.Value)
		}
		var outcome, details string
		if err = e.store.DB().QueryRow(`SELECT outcome,details FROM management_operation_items ORDER BY operation_id DESC,ordinal LIMIT 1`).Scan(&outcome, &details); err != nil {
			t.Fatal(err)
		}
		if outcome != "succeeded" || !strings.Contains(details, "donation_service_health") || !strings.Contains(details, "streak_generation") || !strings.Contains(details, expected) || !strings.Contains(details, "private key note") || !strings.Contains(details, "retained") {
			t.Fatalf("audit: %s %s", outcome, details)
		}
	}
	// Ordinary inference health updates must not produce management snapshots.
	if _, err := e.store.DB().Exec("UPDATE donation_service_health SET failure_streak=nbi_u128(1)"); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := e.store.DB().QueryRow("SELECT count(*) FROM management_capture_rows").Scan(&pending); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
}

func TestServiceFailureResetUnavailableHTTPReason(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "unavailable-owner", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'u')
	d := approvedResetDonation(t, e, owner, physical)
	if _, err := e.store.DB().Exec("UPDATE credential_versions SET enabled=0"); err != nil {
		t.Fatal(err)
	}
	response := resetHTTP(t, &httpAPI{service: e.service}, recurringOwner, owner, "/api/donations/"+d.ID+"/keys/"+d.Keys[0].ID+"/failure-streak-reset", `{"expected_revision":"2"}`, "Z", d.ID, d.Keys[0].ID)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), NoResetServiceMessage) {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	localized := httptest.NewRecorder()
	writeDonationError(httperr.WithLanguage(localized, "zh"), ErrNoResetService)
	if localized.Code != http.StatusConflict || !strings.Contains(localized.Body.String(), "没有符合失败重置条件") {
		t.Fatalf("%d %s", localized.Code, localized.Body)
	}
}
