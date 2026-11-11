package resources

import (
	"context"
	"database/sql"
	"strconv"
)

func baselineEndpointKeyBrowseTx(ctx context.Context, tx *sql.Tx, userID, endpointID int64, key EndpointKey) (*EndpointKeyBrowse, error) {
	keyID, err := parseDecimalID(key.ID)
	if err != nil {
		return nil, err
	}
	evidenceRow, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return nil, err
	}
	evidence, err := evidenceRow.evidence()
	if err != nil {
		return nil, err
	}
	scope := ownedBindingScope + ` AND e.id=? AND k.id=?`
	args := []any{userID, userID, endpointID, keyID}
	// Every target here shares one key. Resolve its live authority once within
	// this snapshot; only the selected service mask varies between targets.
	var availableMask int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT a.native_service_mask FROM self_grant_availability a
WHERE a.endpoint_key_id=? AND a.owner_user_id=?
AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=a.endpoint_key_id)),0)`, keyID, userID).Scan(&availableMask)
	if err != nil {
		return nil, err
	}
	var modelCount, bindingCount, availableCount int64
	countArgs := append([]any{availableMask}, args...)
	err = tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT m.id),COUNT(DISTINCT b.id),COUNT(DISTINCT CASE WHEN (?&t.native_service_mask)>0 THEN b.id ELSE NULL END)`+ownedBindingJoin+scope, countArgs...).Scan(&modelCount, &bindingCount, &availableCount)
	if err != nil {
		return nil, err
	}
	preview, err := bindingPreviewTx(ctx, tx, scope, "t.upstream_model_id,b.id,t.id", args)
	if err != nil {
		return nil, err
	}
	// Submission uses the same persisted suspension and membership checks.
	// A due membership remains occupied until the ordinary expiry path ends it.
	var eligibility string
	err = tx.QueryRowContext(ctx, `SELECT CASE
WHEN EXISTS(SELECT 1 FROM endpoint_key_suspensions WHERE endpoint_key_id=?) THEN 'security_processing'
WHEN EXISTS(SELECT 1 FROM donation_key_memberships WHERE endpoint_key_id=?) THEN 'already_donated'
ELSE 'eligible' END`, keyID, keyID).Scan(&eligibility)
	if err != nil {
		return nil, err
	}
	if !key.CredentialAvailable {
		eligibility = "unavailable"
	}
	return &EndpointKeyBrowse{
		DonationEligibility: eligibility,
		ModelCount:          strconv.FormatInt(modelCount, 10), BindingCount: strconv.FormatInt(bindingCount, 10),
		AvailableBindingCount: strconv.FormatInt(availableCount, 10), Discovery: evidence, Preview: preview,
	}, nil
}
