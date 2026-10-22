package claim

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

func TestConnectionCharityAffinityRestoresOnlyStillApprovedPhysicalVersion(t *testing.T) {
	for _, revokeUse := range []bool{false, true} {
		t.Run(map[bool]string{false: "approved", true: "revoked"}[revokeUse], func(t *testing.T) {
			f, caller, keys, members := routingFixture(t, 300)
			key := keys[0]
			first := routingClaim(t, f, caller, key, members[0])
			dispatchRoutingClaim(t, f, first)
			var owner int64
			if err := f.db.QueryRow(`SELECT user_id FROM endpoints WHERE id=?`, key.endpointID).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			replacement := f.seedKey(owner, "affinity-new-current")
			if _, err := f.db.Exec(`UPDATE endpoint_key_secrets SET key_body_review_hmac=? WHERE id=?`, bytes.Repeat([]byte{7}, 32), replacement.secretID); err != nil {
				t.Fatal(err)
			}
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			result, err := tx.Exec(`INSERT INTO endpoint_key_grant_revisions(endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,rerank_format,report_fingerprint,origin,created_at)
SELECT endpoint_key_id,endpoint_id,owner_user_id,?,?,grant_revision+1,native_service_mask,rerank_format,?,'explicit',? FROM endpoint_key_grant_revisions WHERE id=?`, replacement.candidate.Authorization.CredentialID, replacement.candidate.Authorization.CredentialVersionID, replacement.fingerprint, f.clock.Load(), key.candidate.Authorization.GrantRevisionID)
			if err != nil {
				t.Fatal(err)
			}
			grantID, err := result.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`UPDATE endpoint_keys SET current_grant_revision_id=?,revision=revision+1 WHERE id=?`, grantID, key.keyID); err != nil {
				t.Fatal(err)
			}
			approved := key.charityCandidates[1].Authorization
			var useCAS int64
			if err := tx.QueryRow(`SELECT revision FROM donation_uses WHERE id=?`, approved.UseID).Scan(&useCAS); err != nil {
				t.Fatal(err)
			}
			if _, err := connectionstore.StagePendingUseRevisionTx(t.Context(), tx, connectionstore.StagePendingUseRevisionInput{UseID: approved.UseID, ExpectedRevision: useCAS, GrantRevisionID: grantID, Origin: connectionstore.UseOriginReattachment, At: f.clock.Load()}); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			second := routingClaim(t, f, caller, key, members[0])
			dispatchRoutingClaim(t, f, second)
			if got := routingAffinityAttempt(t, f); got != second.ClaimID() {
				t.Fatal("new dispatch did not own affinity", got)
			}
			if revokeUse {
				if _, err := f.db.Exec(`UPDATE donation_uses SET enabled=0,revision=revision+1 WHERE donation_key_id=?`, members[0]); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.RevokeUndelivered(t.Context(), second); err != nil {
				t.Fatal(err)
			}
			var restored string
			err = f.db.QueryRow(`SELECT attempt_id FROM charity_key_affinities WHERE user_id=? AND model_id=1`, caller).Scan(&restored)
			if revokeUse {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("revoked approval restored affinity: %s %v", restored, err)
				}
			} else if err != nil || restored != first.ClaimID() {
				t.Fatalf("approved old physical affinity lost: %s %v", restored, err)
			}
		})
	}
}
