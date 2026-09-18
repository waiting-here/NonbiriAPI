package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const preGatewayPolicyManifestHash = "d91855671a11b795662ce9619aed66238b1db17cad260b10fb041b3b13026f9a"

const gatewayFailurePolicySchema = `
ALTER TABLE donation_keys ADD COLUMN failure_disable_threshold TEXT NOT NULL DEFAULT '10' CHECK(typeof(failure_disable_threshold)='text' AND instr(failure_disable_threshold,char(0))=0 AND (failure_disable_threshold='0' OR (length(failure_disable_threshold) BETWEEN 1 AND 39 AND failure_disable_threshold NOT GLOB '*[^0-9]*' AND substr(failure_disable_threshold,1,1) BETWEEN '1' AND '9' AND (length(failure_disable_threshold)<39 OR failure_disable_threshold<='340282366920938463463374607431768211455'))));
`

const oldConnectorConstraint = "connector_type IN ('openai-compatible','anthropic-compatible')"
const gatewayConnectorConstraint = "connector_type IN ('openai-compatible','anthropic-compatible','ai-sdk-gateway-v3')"
const oldFailureReviewConstraint = "'member_removed','failure_streak_reset'"
const failurePolicyReviewConstraint = "'member_removed','failure_streak_reset','failure_policy_update'"

func gatewayBootstrapSchema(previous string) string {
	if strings.Count(previous, oldConnectorConstraint) != 7 || strings.Count(previous, oldFailureReviewConstraint) != 1 {
		panic("gateway bootstrap source constraints changed")
	}
	return strings.ReplaceAll(strings.ReplaceAll(previous, oldConnectorConstraint, gatewayConnectorConstraint), oldFailureReviewConstraint, failurePolicyReviewConstraint) + gatewayFailurePolicySchema
}

// The complete source manifest is pinned before changing schema text. Keeping
// the existing table storage preserves credentials, usage and in-flight claims.
func applyGatewayPolicyExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preGatewayPolicyManifestHash {
		return errors.New("unrecognized gateway policy source manifest")
	}
	if _, err := tx.ExecContext(ctx, gatewayFailurePolicySchema); err != nil {
		return err
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("gateway schema version cannot advance")
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	for _, name := range []string{"mainstream_channels", "endpoints", "endpoint_key_secrets", "donation_keys", "request_attempts", "report_cases", "report_targets", "donation_reviews"} {
		var previous string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, name).Scan(&previous); err != nil {
			return err
		}
		before, after := oldConnectorConstraint, gatewayConnectorConstraint
		if name == "donation_reviews" {
			before, after = oldFailureReviewConstraint, failurePolicyReviewConstraint
		}
		if strings.Count(previous, before) != 1 {
			return errors.New("gateway source constraint mismatch")
		}
		result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, strings.Replace(previous, before, after, 1), name, previous)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return errors.New("gateway schema update count mismatch")
		}
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;`, version+1))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO site_config(key,value,updated_at) VALUES('gateway_user_attribution_enabled','0',0)`)
	return err
}
