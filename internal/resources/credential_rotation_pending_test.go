package resources

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// The normal resource fixture omits the review HMAC. Rotation stages real
// donation revisions, which require this vault-produced evidence.
type rotationReviewSecretWriter struct{ *resourceTestSecretWriter }

func (w rotationReviewSecretWriter) WriteCredentialSecret(ctx context.Context, tx *sql.Tx, plaintext []byte, now int64) (StoredSecret, error) {
	value, err := w.resourceTestSecretWriter.WriteCredentialSecret(ctx, tx, plaintext, now)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE endpoint_key_secrets SET key_body_review_hmac=zeroblob(32) WHERE id=?`, value.RefID)
	}
	return value, err
}

func newRotationDonationFixture(t *testing.T) (*connectionTestFixture, int64) {
	t.Helper()
	f := newConnectionTestFixture(t)
	services := []contract.NativeService{contract.NativeServiceChatCompletions, contract.NativeServiceResponses}
	_, first := f.entry(t, f.connection, "https://example.com/v1", services)
	_, second := f.entry(t, f.connection, "https://other.example/v1", services)
	donationID, firstMember := seedDeletionTestMembership(t, f.env, f.user, resourceTestID(t, first.ID), string(contract.TypeOpenAICompatible))
	tx, err := f.env.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(t.Context(), `INSERT INTO donation_keys(
 donation_id,endpoint_key_id,source_endpoint_key_id,report_fingerprint,canonical_base_url,
 price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
 failure_streak,streak_generation,next_claim_seq,next_fold_seq,created_at,updated_at)
 SELECT ?,k.id,k.id,k.secret_fingerprint,e.base_url,
 zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),
 zeroblob(16),X'00000000000000000000000000000001',zeroblob(16),zeroblob(16),?,?
 FROM endpoint_key_current_credentials k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=?`, donationID, resourceTestNow, resourceTestNow, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondMember, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at) VALUES(?,?,?,?)`, second.ID, secondMember, donationID, resourceTestNow); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{firstMember, secondMember} {
		if _, err := testseed.SeedDonationUsesWithDiscoveryTx(t.Context(), tx, id, true, resourceTestNow); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.env.repository.secrets = rotationReviewSecretWriter{f.env.secrets}
	return f, donationID
}

func rotationPendingState(t *testing.T, f *connectionTestFixture) [10]int64 {
	t.Helper()
	var state [10]int64
	err := f.env.store.DB().QueryRow(`SELECT
 (SELECT count(*) FROM endpoint_key_secrets),
 (SELECT count(*) FROM credential_versions),
 (SELECT count(*) FROM endpoint_key_grant_revisions),
 (SELECT count(*) FROM discovery_source_revisions),
 (SELECT count(*) FROM donation_use_revisions),
 (SELECT count(*) FROM donation_uses WHERE current_pending_revision_id IS NOT NULL),
 (SELECT count(*) FROM donation_use_revisions WHERE review_state='approved'),
 (SELECT count(*) FROM ai_agent_review_events),
 (SELECT revision FROM credentials WHERE id=?),
 (SELECT sum(revision) FROM endpoint_keys)`, f.credential).Scan(
		&state[0], &state[1], &state[2], &state[3], &state[4], &state[5], &state[6], &state[7], &state[8], &state[9])
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestCredentialRotationNotifiesPendingDonationAtomically(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "commit_and_replay"
		if rollback {
			name = "callback_failure_and_retry"
		}
		t.Run(name, func(t *testing.T) {
			f, donationID := newRotationDonationFixture(t)
			before := rotationPendingState(t, f)
			calls := 0
			fail := rollback
			callbackErr := errors.New("pending intake failed")
			f.env.repository.pendingSubmission = func(ctx context.Context, tx *sql.Tx, id int64) error {
				calls++
				var pending int
				err := tx.QueryRowContext(ctx, `SELECT count(*) FROM donation_uses u
 JOIN donation_use_revisions ur ON ur.id=u.current_pending_revision_id
 JOIN donation_keys dk ON dk.id=u.donation_key_id
 JOIN endpoint_keys k ON k.id=dk.endpoint_key_id
 WHERE dk.donation_id=? AND ur.origin='rotation' AND ur.review_state='pending'
 AND ur.credential_version=2 AND ur.grant_revision_id=k.current_grant_revision_id`, id).Scan(&pending)
				if err != nil {
					return err
				}
				if id != donationID || pending != 4 {
					t.Fatalf("notification did not see the whole rotation: donation=%d pending=%d", id, pending)
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO ai_agent_review_events(donation_id,sequence,mode,created_at,updated_at) VALUES(?,1,'auto',?,?)`, id, resourceTestNow*1000, resourceTestNow*1000); err != nil {
					return err
				}
				if fail {
					return callbackErr
				}
				return nil
			}
			input := RotateCredentialInput{Secret: []byte("rotation-pending-test-credential"), OwnershipConfirmed: true, ExpectedRevision: 1}
			mutation := f.mutation(t, "POST", routeCredentialVersions, []int64{f.credential}, input)
			if rollback {
				if _, err := f.env.repository.RotateCredential(t.Context(), f.user, f.credential, mutation, input); !errors.Is(err, callbackErr) {
					t.Fatalf("callback failure: %v", err)
				}
				if after := rotationPendingState(t, f); after != before {
					t.Fatalf("failed notification committed part of rotation: before=%v after=%v", before, after)
				}
				fail = false
			}
			result, err := f.env.repository.RotateCredential(t.Context(), f.user, f.credential, mutation, input)
			if err != nil || result.Replayed || result.Value.Credential.CredentialVersion != "2" {
				t.Fatalf("rotation: %+v %v", result, err)
			}
			wantCalls := 1
			if rollback {
				wantCalls++
			}
			if calls != wantCalls {
				t.Fatalf("multiple keys notified the same donation more than once: got %d want %d", calls, wantCalls)
			}
			after := rotationPendingState(t, f)
			if after[5] != 4 || after[6] != 4 || after[7] != 1 {
				t.Fatalf("pending event/approved versions not preserved: %v", after)
			}
			replay, err := f.env.repository.RotateCredential(t.Context(), f.user, f.credential, mutation, input)
			if err != nil || !replay.Replayed || !bytes.Equal(replay.Body, result.Body) || calls != wantCalls || rotationPendingState(t, f) != after {
				t.Fatalf("replay repeated rotation or notification: %+v calls=%d err=%v", replay, calls, err)
			}
		})
	}
}

func TestPendingSubmissionSkipsAlreadyApprovedDonation(t *testing.T) {
	f, _ := newRotationDonationFixture(t)
	f.env.repository.pendingSubmission = func(context.Context, *sql.Tx, int64) error {
		t.Fatal("an approved donation with no pending revision was notified")
		return nil
	}
	tx, err := f.env.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var first, second int64
	if err := tx.QueryRowContext(t.Context(), `SELECT min(current_grant_revision_id),max(current_grant_revision_id) FROM endpoint_keys`).Scan(&first, &second); err != nil {
		t.Fatal(err)
	}
	if err := f.env.repository.notifyPendingSubmissionsTx(t.Context(), tx, f.user, []int64{first, second}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
