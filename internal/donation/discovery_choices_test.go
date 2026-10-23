package donation

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestManagedDiscoveryLegacySourceGrouping(t *testing.T) {
	for _, scenario := range []string{"shared", "first disabled", "first cooling", "all cooling"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDiscoveryFixture(t, false, contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings)
			want := contract.NativeServiceChatCompletions
			if scenario == "first disabled" {
				if _, err := f.store.DB().Exec(`UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE donation_key_id=? AND native_service_bit=1`, f.donationKey); err != nil {
					t.Fatal(err)
				}
				want = contract.NativeServiceEmbeddings
			}
			if scenario == "first cooling" || scenario == "all cooling" {
				filter := " AND u.native_service_bit=1"
				if scenario == "all cooling" {
					filter = ""
				}
				_, err := f.store.DB().Exec(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,retry_after_until,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT u.id,dk.endpoint_key_id,u.native_service_bit,g.credential_version_id,nbi_u128(1),nbi_u128(0),nbi_u128(1),nbi_u128(1),?,1,0,0,?
FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=ur.grant_revision_id
WHERE u.donation_key_id=?`+filter, f.clock.Load()+60, f.clock.Load(), f.donationKey)
				if err != nil {
					t.Fatal(err)
				}
				want = contract.NativeServiceEmbeddings
			}
			page, err := f.service.selectDiscoveries(context.Background(), reviewerAdmin, 0, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := f.start(t, "admin", f.admin, 'S')
			if scenario == "all cooling" {
				if !errors.Is(err, resources.ErrResourceLocked) || len(page.Items) != 0 || f.rail.calls.Load() != 0 {
					t.Fatalf("unhealthy uses selected: page=%+v start=%v calls=%d", page, err, f.rail.calls.Load())
				}
				return
			}
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("shared source refused: page=%+v start=%v", page, err)
			}
			f.wait(t, accepted.Value.OperationID)
			var selectedBit int64
			if err := f.store.DB().QueryRow(`SELECT u.native_service_bit FROM discovery_runs r JOIN donation_uses u ON u.id=r.authorization_use_id WHERE r.operation_id=?`, accepted.Value.OperationID).Scan(&selectedBit); err != nil {
				t.Fatal(err)
			}
			set, err := contract.NativeServicesFromMask(selectedBit)
			if err != nil || !set.Has(want) || f.rail.calls.Load() != 1 {
				t.Fatalf("wrong exact use dispatched: bit=%d want=%s calls=%d err=%v", selectedBit, want, f.rail.calls.Load(), err)
			}
		})
	}
}

func TestManagedDiscoveryDistinctApprovedSourcesRequireSelection(t *testing.T) {
	f := newDiscoveryFixture(t, false, contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings)
	ctx := context.Background()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	use, err := connectionstore.LoadApprovedUseTx(ctx, tx, f.donationKey, contract.NativeServiceEmbeddings)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO discovery_source_revisions(source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at)
SELECT source_id,owner_user_id,source_revision+1,credential_id,credential_version_id,canonical_base_url||'/alternate',list_format,created_at FROM discovery_source_revisions WHERE id=?`, use.DiscoverySourceRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	sourceRevision, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE discovery_sources SET current_revision_id=?,revision=revision+1 WHERE current_revision_id=?`, sourceRevision, use.DiscoverySourceRevisionID); err != nil {
		t.Fatal(err)
	}
	staged, err := connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{UseID: use.UseID, ExpectedRevision: use.UseRevision, GrantRevisionID: use.Grant.GrantRevisionID, DiscoverySourceRevisionID: &sourceRevision, Origin: connectionstore.UseOriginServiceExpansion, At: f.clock.Load()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	items := []UseReviewItem{{UseID: use.UseID, ExpectedRevision: staged.UseRevision, Decision: "approve", ReviewNote: "updated discovery source"}}
	review, err := f.service.reviewUses(ctx, reviewerAdmin, 0, f.donation, items, donationMutation(t, 'T', http.MethodPost, routeAdminUses+"/review", []int64{f.donation}, items))
	if err != nil || len(review.Value.Results) != 1 || !review.Value.Results[0].OK {
		t.Fatalf("approve exact updated source: %+v %v", review, err)
	}
	page, err := f.service.selectDiscoveries(ctx, reviewerAdmin, 0, 0, "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("ambiguous member offered by legacy selection: %+v %v", page, err)
	}
	if _, err := f.start(t, "admin", f.admin, 'U'); !errors.Is(err, resources.ErrConflict) {
		t.Fatalf("different approved sources accepted without selection: %v", err)
	}
	tx, err = f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	uses, err := connectionstore.LoadApprovedUsesTx(ctx, tx, []int64{f.donationKey})
	if err != nil || len(uses[f.donationKey]) != 2 {
		t.Fatal("approved uses lost", err)
	}
	for _, approved := range uses[f.donationKey] {
		target, err := f.service.AuthorizeManagedDiscovery(ctx, tx, "admin", f.admin, f.donation, f.donationKey, resources.ManagedDiscoverySelection{UseID: approved.UseID, ExpectedRevision: approved.UseRevision}, true)
		if err != nil || target.SourceRevisionID != approved.DiscoverySourceRevisionID || target.CredentialVersionID != approved.Grant.CredentialVersionID {
			t.Fatalf("exact selection failed: %+v %v", target, err)
		}
	}
}
