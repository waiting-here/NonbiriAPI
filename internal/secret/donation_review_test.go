package secret_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestDonationReviewStableRestartRewrapAndMissingMaterial(t *testing.T) {
	ctx := context.Background()
	vault, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "review.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	review, err := secret.NewDonationReview(vault)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.Initialize(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	seed := func(v *secret.Vault, r *secret.DonationReview, index byte, body, url, kind string) int64 {
		t.Helper()
		contextID := make([]byte, 16)
		contextID[15] = index
		credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := v.SealForGenerationTwoContext([]byte(body), credentialContext)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		result, err := tx.Exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,?,?,?)`, contextID, url, kind, encoded, 1700000000)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		if err = r.BackfillEndpointSecrets(ctx, tx, []int64{id}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := seed(vault, review, 1, "Exact Body", "https://one.example.test/v1", "openai-compatible")
	restarted, err := secret.NewDonationReview(vault)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.Initialize(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	second := seed(vault, restarted, 2, "Exact Body", "https://two.example.test", "anthropic-compatible")
	equal := func(a, b int64, want bool) {
		t.Helper()
		var matches bool
		if err := store.DB().QueryRow(`SELECT a.key_body_review_hmac=b.key_body_review_hmac FROM endpoint_key_secrets a,endpoint_key_secrets b WHERE a.id=? AND b.id=?`, a, b).Scan(&matches); err != nil || matches != want {
			t.Fatalf("body identity comparison=%v want=%v error=%v", matches, want, err)
		}
	}
	equal(first, second, true)
	different := seed(vault, review, 3, "exact Body", "https://one.example.test/v1", "openai-compatible")
	equal(first, different, false)
	target, err := secret.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.RewrapMaterial(ctx, tx, target, time.Now().Add(time.Second)); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rotated, err := secret.NewDonationReview(target)
	if err != nil {
		t.Fatal(err)
	}
	if err = rotated.Initialize(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	fourth := seed(target, rotated, 4, "Exact Body", "https://restored.example.test", "anthropic-compatible")
	equal(first, fourth, true)
	var envelope []byte
	if err = store.DB().QueryRow(`SELECT envelope FROM donation_review_material`).Scan(&envelope); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB().Exec(`DELETE FROM donation_review_material`); err != nil {
		t.Fatal(err)
	}
	if err = rotated.Initialize(ctx, store.DB()); !errors.Is(err, secret.ErrReviewMaterial) {
		t.Fatalf("missing replacement: %v", err)
	}
	var rows int
	if err = store.DB().QueryRow(`SELECT count(*) FROM donation_review_material`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("missing material was replaced")
	}
	if _, err = store.DB().Exec(`INSERT INTO donation_review_material(id,version,envelope,created_at,updated_at) VALUES(1,1,?,?,?)`, envelope, time.Now().Unix(), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err = rotated.Initialize(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB().Exec(`UPDATE donation_review_material SET envelope=zeroblob(64)`); err != nil {
		t.Fatal(err)
	}
	if err = rotated.Initialize(ctx, store.DB()); !errors.Is(err, secret.ErrReviewMaterial) {
		t.Fatalf("corrupt material: %v", err)
	}
}

func TestDonationReviewLegacyBackfillOriginalSources(t *testing.T) {
	ctx := context.Background()
	master := bytes.Repeat([]byte{11}, 32)
	vault, err := secret.New(master)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	review, err := secret.NewDonationReview(vault)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.Initialize(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	const at = int64(1700000000)
	const url = "https://legacy.example.test/v1"
	const kind = "openai-compatible"
	execID := func(query string, args ...any) int64 {
		t.Helper()
		result, err := store.DB().Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	seedSecret := func(index byte, body, address, connector string, created int64) int64 {
		t.Helper()
		contextID := make([]byte, 16)
		contextID[15] = index
		credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := vault.SealForGenerationTwoContext([]byte(body), credentialContext)
		if err != nil {
			t.Fatal(err)
		}
		return execID(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,?,?,?)`, contextID, address, connector, encoded, created)
	}
	original := seedSecret(1, "original-body", url, kind, at)
	current := seedSecret(2, "replacement-body", url, kind, at+20)
	detached := seedSecret(3, "保留正文", "https://detached.example.test", "anthropic-compatible", at)
	future := seedSecret(4, "future-body", url, kind, at+40)
	sameBody := seedSecret(5, "original-body", "https://other.example.test", kind, at)
	user := execID(`INSERT INTO users(donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?)`, at, at)
	endpoint := execID(`INSERT INTO endpoints(user_id,connector_type,base_url,created_at,updated_at) VALUES(?,?,?,?,?)`, user, kind, url, at, at)
	physical := execID(`INSERT INTO endpoint_keys(endpoint_id,secret_ref_id,secret_fingerprint,created_at,updated_at) VALUES(?,?,zeroblob(32),?,?)`, endpoint, current, at, at+20)
	donation := execID(`INSERT INTO donations(user_id,status,revision,created_at,updated_at) VALUES(?,'pending',1,?,?)`, user, at, at)
	seedMember := func(body, address, connector string, created int64, active bool, known bool) int64 {
		t.Helper()
		// This independent oracle is the prior migration fingerprint contract.
		fingerprint, err := db.ComputeCredentialReportFingerprint(master, connector, address, body)
		if err != nil {
			t.Fatal(err)
		}
		generation := make([]byte, 16)
		generation[15] = 1
		var key, ended, reason, matchUntil, digest, revision any
		enabled := 0
		if active {
			key, enabled = physical, 1
		} else {
			ended, reason, matchUntil = created+1, "terminated", created+1+7776000
		}
		if known {
			digest, revision = make([]byte, 32), 3
		}
		return execID(`INSERT INTO donation_keys(donation_id,endpoint_key_id,source_endpoint_key_id,canonical_base_url,connector_type,report_fingerprint,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,failure_streak,streak_generation,next_claim_seq,next_fold_seq,enabled,created_at,updated_at,ended_at,ended_reason,report_match_until,key_body_review_hmac,review_revision)
VALUES(?,?,?,?,?,?,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,zeroblob(16),zeroblob(16),?,?,?,?,?,?,?,?)`, donation, key, physical, address, connector, fingerprint[:], generation, enabled, created, created, ended, reason, matchUntil, digest, revision)
	}
	active := seedMember("original-body", url, kind, at, true, false)
	disconnected := seedMember("original-body", url, kind, at+11, false, false)
	retained := seedMember("保留正文", "https://detached.example.test", "anthropic-compatible", at+10, false, false)
	unknown := seedMember("no-retained-body", url, kind, at+10, false, false)
	tooEarly := seedMember("future-body", url, kind, at+10, false, false)
	known := seedMember("original-body", url, kind, at+10, false, true)
	wrongURL := seedMember("original-body", "https://unretained.example.test", kind, at+10, false, false)
	wrongKind := seedMember("original-body", url, "anthropic-compatible", at+10, false, false)
	run := func(ids []int64, commit bool) error {
		t.Helper()
		tx, err := store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = review.BackfillLegacySecrets(ctx, tx, ids, time.Unix(at+100, 0)); err != nil {
			return err
		}
		if commit {
			return tx.Commit()
		}
		return nil
	}
	// An existing requirement must be copied into newly proven snapshots.
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = review.BackfillEndpointSecrets(ctx, tx, []int64{sameBody}, time.Unix(at, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO donation_key_review_requirements(hmac,required,revision,created_at,updated_at) SELECT key_body_review_hmac,1,7,?,? FROM endpoint_key_secrets WHERE id=?`, at, at, sameBody); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertUnknown := func(id int64) {
		t.Helper()
		var missing bool
		if err := store.DB().QueryRow(`SELECT key_body_review_hmac IS NULL AND review_revision IS NULL FROM donation_keys WHERE id=?`, id).Scan(&missing); err != nil || !missing {
			t.Fatalf("unproven member changed: %v", err)
		}
	}
	if err = run([]int64{original}, false); err != nil {
		t.Fatal(err)
	}
	assertUnknown(active)
	var missing bool
	if err = store.DB().QueryRow(`SELECT key_body_review_hmac IS NULL FROM endpoint_key_secrets WHERE id=?`, original).Scan(&missing); err != nil || !missing {
		t.Fatal("rolled-back source changed")
	}
	if err = run([]int64{original, 9223372036854775807}, true); err == nil {
		t.Fatal("missing batch source accepted")
	}
	assertUnknown(active)
	if err = run(make([]int64, 1001), true); !errors.Is(err, secret.ErrReviewMaterial) {
		t.Fatalf("unbounded batch: %v", err)
	}
	if err = run([]int64{original, current, detached, future}, true); err != nil {
		t.Fatal(err)
	}
	assertOriginal := func(id int64) {
		t.Helper()
		var matches bool
		if err := store.DB().QueryRow(`SELECT dk.key_body_review_hmac=s.key_body_review_hmac AND dk.review_revision=7 FROM donation_keys dk,endpoint_key_secrets s WHERE dk.id=? AND s.id=?`, id, sameBody).Scan(&matches); err != nil || !matches {
			t.Fatalf("original snapshot mismatch: %v", err)
		}
	}
	assertOriginal(active)
	assertOriginal(disconnected)
	var retainedMatches bool
	if err = store.DB().QueryRow(`SELECT dk.key_body_review_hmac=s.key_body_review_hmac AND dk.review_revision IS NULL FROM donation_keys dk,endpoint_key_secrets s WHERE dk.id=? AND s.id=?`, retained, detached).Scan(&retainedMatches); err != nil || !retainedMatches {
		t.Fatalf("detached original not proven: %v", err)
	}
	assertUnknown(unknown)
	assertUnknown(tooEarly)
	assertUnknown(wrongURL)
	assertUnknown(wrongKind)
	var different bool
	if err = store.DB().QueryRow(`SELECT a.key_body_review_hmac<>b.key_body_review_hmac FROM endpoint_key_secrets a,endpoint_key_secrets b WHERE a.id=? AND b.id=?`, original, current).Scan(&different); err != nil || !different {
		t.Fatal("replacement body polluted original identity")
	}
	var preserved bool
	if err = store.DB().QueryRow(`SELECT key_body_review_hmac=zeroblob(32) AND review_revision=3 FROM donation_keys WHERE id=?`, known).Scan(&preserved); err != nil || !preserved {
		t.Fatal("known member overwritten")
	}
	if _, err = store.DB().Exec(`UPDATE donation_key_review_requirements SET revision=8`); err != nil {
		t.Fatal(err)
	}
	if err = run([]int64{original, current, detached, future}, true); err != nil {
		t.Fatal(err)
	}
	assertOriginal(active)
	assertOriginal(disconnected)
	// Missing stable material must fail even on a replay, never initialize it.
	if _, err = store.DB().Exec(`DELETE FROM donation_review_material`); err != nil {
		t.Fatal(err)
	}
	if err = run([]int64{original}, true); !errors.Is(err, secret.ErrReviewMaterial) {
		t.Fatalf("missing review material: %v", err)
	}
	var materialCount int
	if err = store.DB().QueryRow(`SELECT count(*) FROM donation_review_material`).Scan(&materialCount); err != nil || materialCount != 0 {
		t.Fatal("lost material was recreated")
	}
}
