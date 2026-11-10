package v1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var errDiscoveryURLTooLong = errors.New("complete model-list address exceeds 4096 bytes")

const legacyDiscoveryURLConversion = `CASE
 WHEN substr(rtrim(canonical_base_url,'/'),-7)=CASE WHEN list_format IN ('ai-sdk-gateway-v3','ai-sdk-gateway-v4') THEN '/config' ELSE '/models' END THEN canonical_base_url
 ELSE rtrim(canonical_base_url,'/')||CASE WHEN list_format IN ('ai-sdk-gateway-v3','ai-sdk-gateway-v4') THEN '/config' ELSE '/models' END END`

// Complete addresses replace the old base-address interpretation in place so
// donated pins, historical runs and credential-version authorizations retain
// their identities. Only the explicit offline transaction can rewrite them.
func completeDiscoveryURLs(ctx context.Context, tx *sql.Tx) error {
	if err := checkCompleteDiscoveryURLs(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, db.CompleteDiscoveryURLSQL); err != nil {
		return err
	}
	var guard string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='discovery_source_revision_immutable'`).Scan(&guard); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP TRIGGER discovery_source_revision_immutable"); err != nil {
		return err
	}
	for _, table := range []string{"discovery_source_revisions", "mainstream_channel_discovery_presets"} {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET canonical_base_url=`+legacyDiscoveryURLConversion+` WHERE canonical_base_url<>(`+legacyDiscoveryURLConversion+`)`); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, guard)
	return err
}

func checkCompleteDiscoveryURLs(ctx context.Context, q queryer) error {
	var sourceID, revision int64
	err := q.QueryRowContext(ctx, `SELECT source_id,source_revision FROM discovery_source_revisions WHERE length(CAST((`+legacyDiscoveryURLConversion+`) AS BLOB))>4096 ORDER BY source_id,source_revision LIMIT 1`).Scan(&sourceID, &revision)
	if err == nil {
		return fmt.Errorf("%w: discovery source %d revision %d requires a shorter address before upgrading", errDiscoveryURLTooLong, sourceID, revision)
	}
	if err != sql.ErrNoRows {
		return err
	}
	var channel string
	err = q.QueryRowContext(ctx, `SELECT channel_id FROM mainstream_channel_discovery_presets WHERE length(CAST((`+legacyDiscoveryURLConversion+`) AS BLOB))>4096 ORDER BY channel_id LIMIT 1`).Scan(&channel)
	if err == nil {
		return fmt.Errorf("%w: channel %s discovery preset requires a shorter address before upgrading", errDiscoveryURLTooLong, channel)
	}
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}
