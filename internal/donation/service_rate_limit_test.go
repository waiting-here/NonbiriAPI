package donation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestServiceRateLimitReleasePreservesStateAndReplays(t *testing.T) {
	for _, role := range []reviewerRole{recurringOwner, reviewerAdmin, reviewerSteward} {
		t.Run(string(role), func(t *testing.T) {
			e := newDonationTestEnv(t)
			e.seedUser(t, "", nil, true)
			owner := e.seedUser(t, "release-owner", nil, false)
			level := int64(6)
			steward := e.seedUser(t, "release-manager", &level, false)
			_, physical := e.seedEndpointKey(t, owner, 'r')
			d := approvedResetDonation(t, e, owner, physical)
			id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
			useID, revision := seedResetHealth(t, e, key)
			if _, err := e.store.DB().Exec("UPDATE donation_keys SET enabled=0,expires_at=?,price_limit_mag=nbi_u128(100),call_limit_mag=nbi_u128(50),token_limit_mag=nbi_u128(1000),token_reserve=7 WHERE id=?", donationTestNow+3600, key); err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.DB().Exec("INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm) VALUES(?,3,7) ON CONFLICT(endpoint_key_id) DO UPDATE SET max_concurrency=3,max_rpm=7", physical); err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.DB().Exec("UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE id=?", useID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.DB().Exec("INSERT INTO donation_key_cooldowns(donation_key_id,rate_limited_until,updated_at) VALUES(?,?,?)", key, donationTestNow+90, donationTestNow); err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.DB().Exec("INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,period,source,stage,retry_at,updated_at) VALUES(?,4,2,'day','builtin',1,?,?)", key, donationTestNow+120, donationTestNow); err != nil {
				t.Fatal(err)
			}
			snapshot := func() string {
				var value string
				query := `SELECT json_object('key',json_array(hex(dk.failure_streak),dk.failure_disabled,dk.enabled,dk.expires_at,hex(dk.price_limit_mag),hex(dk.call_limit_mag),hex(dk.token_limit_mag),dk.token_reserve,hex(dk.price_used_mag),hex(dk.calls_used),hex(dk.tokens_used),(SELECT json_array(l.max_concurrency,l.max_rpm) FROM endpoint_key_limits l WHERE l.endpoint_key_id=dk.endpoint_key_id)),'health',json_array(hex(h.streak_generation),hex(h.failure_streak),h.failure_disabled,hex(h.next_claim_seq),hex(h.next_fold_seq),u.enabled),'other',(SELECT group_concat(hex(x.failure_streak)||':'||x.failure_disabled||':'||x.retry_after_until||':'||x.backoff_retry_at) FROM donation_service_health x WHERE x.use_id<>u.id)) FROM donation_keys dk JOIN donation_uses u ON u.donation_key_id=dk.id JOIN donation_service_health h ON h.use_id=u.id WHERE u.id=?`
				if err := e.store.DB().QueryRow(query, useID).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			revision++
			before := snapshot()
			actor := int64(0)
			if role == recurringOwner {
				actor = owner
			} else if role == reviewerSteward {
				actor = steward
			}
			ctx := authz.WithStewardPermission(context.Background(), "donations.manage_keys")
			mutation := donationMutation(t, 'L', http.MethodPost, useRoute(role)+"/{useId}/rate-limit-release", []int64{id, useID}, map[string]string{"expected_revision": fmt.Sprint(revision)})
			results := make(chan resources.MutationResult[Use], 2)
			failures := make(chan error, 2)
			var group sync.WaitGroup
			for range 2 {
				group.Go(func() {
					out, err := e.service.mutateUse(ctx, role, actor, id, useID, UsePatch{ExpectedRevision: revision}, "release_rate_limit", mutation)
					results <- out
					failures <- err
				})
			}
			group.Wait()
			for range 2 {
				if err := <-failures; err != nil {
					t.Fatal(err)
				}
			}
			first, second := <-results, <-results
			if first.Replayed == second.Replayed || !bytes.Equal(first.Body, second.Body) {
				t.Fatal("concurrent replay did not retain receipt")
			}
			if first.Value.Health == nil || first.Value.Health.RetryAfterUntil != nil || first.Value.Health.BackoffRetryAt != nil || first.Value.Revision != fmt.Sprint(revision+1) {
				t.Fatalf("release receipt: %+v", first.Value)
			}
			if snapshot() != before {
				t.Fatal("release changed independent state")
			}
			var generation, episode, serviceGeneration, cooldowns int
			query := "SELECT generation,episode,(SELECT backoff_generation FROM donation_service_health WHERE use_id=?),(SELECT count(*) FROM donation_key_cooldowns WHERE donation_key_id=?) FROM donation_key_backoffs WHERE donation_key_id=?"
			if err := e.store.DB().QueryRow(query, useID, key, key).Scan(&generation, &episode, &serviceGeneration, &cooldowns); err != nil {
				t.Fatal(err)
			}
			if generation != 5 || episode != 0 || serviceGeneration != 4 || cooldowns != 0 {
				t.Fatal(generation, episode, serviceGeneration, cooldowns)
			}
			if role == reviewerSteward {
				if _, err := e.store.DB().Exec("UPDATE users SET level=4 WHERE id=?", actor); err != nil {
					t.Fatal(err)
				}
				if _, err := e.service.mutateUse(ctx, role, actor, id, useID, UsePatch{ExpectedRevision: revision}, "release_rate_limit", mutation); !errors.Is(err, ErrForbidden) {
					t.Fatal("demoted replay accepted", err)
				}
			}
		})
	}
}

func TestServiceRateLimitReleaseChecksOwnershipPermissionAndEligibility(t *testing.T) {
	for _, blocked := range []string{"foreign", "permission", "expired", "review", "credential"} {
		t.Run(blocked, func(t *testing.T) {
			e := newDonationTestEnv(t)
			e.seedUser(t, "", nil, true)
			owner := e.seedUser(t, "release-owner", nil, false)
			_, physical := e.seedEndpointKey(t, owner, 'c')
			d := approvedResetDonation(t, e, owner, physical)
			id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
			useID, revision := seedResetHealth(t, e, key)
			role, actor := recurringOwner, owner
			ctx := authz.WithStewardPermission(context.Background(), "donations.manage_keys")
			want := ErrConflict
			switch blocked {
			case "foreign":
				actor = e.seedUser(t, "other-owner", nil, false)
				want = ErrNotFound
			case "permission":
				level := int64(5)
				role, actor = reviewerSteward, e.seedUser(t, "revoked-manager", &level, false)
				policy := authz.DefaultStewardPermissions(5)
				policy.Permissions["donations.manage_keys"] = false
				permissions, _ := json.Marshal(policy.Permissions)
				limits, _ := json.Marshal(policy.Limits)
				query := "INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'full',?,?)"
				if _, err := e.store.DB().Exec(query, actor, string(permissions), string(limits), donationTestNow, donationTestNow); err != nil {
					t.Fatal(err)
				}
				want = authz.ErrPermissionDenied
			case "expired":
				if _, err := e.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE id=?", donationTestNow, key); err != nil {
					t.Fatal(err)
				}
			case "review":
				query := `INSERT INTO donation_key_review_requirements(hmac,required,revision,created_at,updated_at) SELECT DISTINCT key_body_review_hmac,1,1,?,? FROM donation_use_revisions WHERE use_id=?`
				if _, err := e.store.DB().Exec(query, donationTestNow, donationTestNow, useID); err != nil {
					t.Fatal(err)
				}
			case "credential":
				if _, err := e.store.DB().Exec("UPDATE credential_versions SET enabled=0"); err != nil {
					t.Fatal(err)
				}
			}
			mutation := donationMutation(t, 'B', http.MethodPost, useRoute(role)+"/{useId}/rate-limit-release", []int64{id, useID}, map[string]string{"expected_revision": fmt.Sprint(revision)})
			_, err := e.service.mutateUse(ctx, role, actor, id, useID, UsePatch{ExpectedRevision: revision}, "release_rate_limit", mutation)
			if !errors.Is(err, want) {
				t.Fatal("blocked release", err, want)
			}
			var until int64
			if err := e.store.DB().QueryRow("SELECT retry_after_until FROM donation_service_health WHERE use_id=?", useID).Scan(&until); err != nil || until != donationTestNow+60 {
				t.Fatal(until, err)
			}
		})
	}
}

func TestServiceRateLimitReleaseTraineeAuditAndRevocation(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "audit-release-owner", nil, false)
	level := int64(5)
	actor := e.seedUser(t, "audit-release-manager", &level, false)
	_, physical := e.seedEndpointKey(t, owner, 'm')
	d := approvedResetDonation(t, e, owner, physical)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	useID, revision := seedResetHealth(t, e, key)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["charity.non_mainstream"] = true
	writePolicy := func() {
		permissions, _ := json.Marshal(policy.Permissions)
		limits, _ := json.Marshal(policy.Limits)
		query := "INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'full',?,?) ON CONFLICT(user_id) DO UPDATE SET permissions=excluded.permissions,revision=steward_permissions.revision+1"
		if _, err := e.store.DB().Exec(query, actor, string(permissions), string(limits), donationTestNow, donationTestNow); err != nil {
			t.Fatal(err)
		}
	}
	writePolicy()
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
	ctx := authz.WithStewardPermission(context.Background(), "donations.manage_keys")
	ctx = managementlog.WithOperation(ctx, managementlog.Metadata{Source: managementlog.SourceWeb, ExecutorKind: "human", Actor: managementlog.Subject{ID: fmt.Sprint(actor), Name: "Steward", Role: "trainee5"}, Domain: "donations", Action: "rate_limit.release"})
	mutation := donationMutation(t, 'A', http.MethodPost, useRoute(reviewerSteward)+"/{useId}/rate-limit-release", []int64{id, useID}, map[string]string{"expected_revision": fmt.Sprint(revision)})
	if _, err := e.service.mutateUse(ctx, reviewerSteward, actor, id, useID, UsePatch{ExpectedRevision: revision}, "release_rate_limit", mutation); err != nil {
		t.Fatal(err)
	}
	var details string
	if err := e.store.DB().QueryRow("SELECT details FROM management_operation_items ORDER BY operation_id DESC,ordinal LIMIT 1").Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, "donation_service_health") || !strings.Contains(details, "retry_after_until") {
		t.Fatal("release audit lacks changed wait", details)
	}
	policy.Permissions["donations.manage_keys"] = false
	writePolicy()
	if _, err := e.service.mutateUse(ctx, reviewerSteward, actor, id, useID, UsePatch{ExpectedRevision: revision}, "release_rate_limit", mutation); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("revoked replay", err)
	}
}
