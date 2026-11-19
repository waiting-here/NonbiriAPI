package claim

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

type ModelQuotaLimit struct {
	Reason        string
	BindingID     int64
	NativeService contract.NativeService
}

func (*ModelQuotaLimit) Error() string { return ErrKeyRateLimited.Error() }
func (*ModelQuotaLimit) Unwrap() error { return ErrKeyRateLimited }

func googleQuotaResponse(record claimRecord, status int) bool {
	return status == 429 && upstreamerror.IsGoogleAPI(record.connectorType == contract.TypeGoogleGemini, record.baseURL)
}

// The service and model are read from the immutable dispatch target. An
// operator release fences earlier receipts even if they finish afterwards.
func storeModelCooldownTx(ctx context.Context, tx *sql.Tx, claimID, reason string, until int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO donation_service_model_cooldowns(health_id,upstream_model_id,reason,retry_at)
SELECT h.id,t.upstream_model_id,?,? FROM dispatch_claim_targets t
JOIN dispatch_service_health sh ON sh.claim_id=t.claim_id
JOIN donation_service_health h ON h.id=sh.health_id
JOIN dispatch_smart_backoff_facts f ON f.claim_id=t.claim_id
JOIN donation_uses u ON u.id=h.use_id AND u.current_approved_revision_id=t.use_revision_id_snapshot
JOIN charity_use_availability a ON a.use_id=h.use_id AND a.credential_version_id=h.credential_version_id
 AND a.grant_revision_id=t.grant_revision_id_snapshot AND a.donation_key_id=f.donation_key_id
JOIN charity_binding_targets target ON target.id=t.target_id_snapshot AND target.revision=t.target_revision AND target.donation_key_id=a.donation_key_id
 AND target.upstream_model_id=t.upstream_model_id AND (target.native_service_mask & h.native_service_bit)<>0
WHERE t.claim_id=? AND ( (f.health_id_snapshot=h.id AND h.backoff_generation=f.generation)
 OR (f.health_id_snapshot IS NULL AND EXISTS(SELECT 1 FROM donation_key_backoffs kb WHERE kb.donation_key_id=f.donation_key_id AND kb.generation=f.generation)))
ON CONFLICT(health_id,upstream_model_id) DO UPDATE SET reason=excluded.reason,retry_at=excluded.retry_at
WHERE excluded.retry_at>donation_service_model_cooldowns.retry_at`, reason, until, claimID)
	return err
}

// A provisional Google 429 header gates the service while its body is being
// read. Finalize just this claim; overlapping headers keep their own expiry.
func finishGoogleHeaderTx(ctx context.Context, tx *sql.Tx, record claimRecord, reason string, observed, until int64) error {
	if until > observed {
		if reason != "" {
			if err := storeModelCooldownTx(ctx, tx, record.claimID, reason, until); err != nil {
				return err
			}
		} else if err := smartbackoff.ObserveRetryAfter(ctx, tx, record.claimID, until, observed); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE dispatch_retry_after_observations SET pending_health_id=NULL WHERE claim_id=? AND pending_health_id IS NOT NULL`, record.claimID)
	return err
}
