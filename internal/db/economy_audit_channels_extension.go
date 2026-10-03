package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const preEconomyAuditChannelsManifestHash = "18af84b0618afaed94c39c8470af3d1e892b365e238b3fc5a2158a2043930d34"
const economyAuditChannelsBefore = "'picture_book','inactivity','penalty','unclassified'"
const economyAuditChannelsAfter = "'picture_book','inactivity','penalty','fat_fish','lake_notes','unclassified'"

// Keep the exact released predecessor as the input to historical extensions.
func preEconomyAuditChannelsSchema() string {
	return strings.Replace(generationTwoSchema, economyAuditChannelsAfter, economyAuditChannelsBefore, 1)
}

func applyEconomyAuditChannelsExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preEconomyAuditChannelsManifestHash {
		return errors.New("unrecognized economy audit channels source manifest")
	}
	// The existing projector resumes from the unchanged checkpoint after startup.
	return rebuildStorageContractTable(ctx, tx, "economy_audit_buckets", economyAuditChannelsBefore, economyAuditChannelsAfter, 1)
}
