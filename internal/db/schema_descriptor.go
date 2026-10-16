package db

import "context"

type StorageSchemaDescriptor struct {
	FormatVersion int    `json:"format_version"`
	ApplicationID uint32 `json:"application_id"`
	UserVersion   uint32 `json:"user_version"`
	GenerationTwoCompatibility
	OfflineConversions []OfflineConversionDescriptor `json:"offline_conversions"`
}

type OfflineConversionDescriptor struct {
	Tool                string `json:"tool"`
	SourceCommit        string `json:"source_commit"`
	SourceSchemaVersion int    `json:"source_schema_version"`
	SourceManifestHash  string `json:"source_manifest_hash"`
	TargetSchemaVersion int    `json:"target_schema_version"`
}

// SchemaDescriptor reads compiled metadata only; no environment or database
// is needed by deployment tooling.
func SchemaDescriptor() StorageSchemaDescriptor {
	return StorageSchemaDescriptor{FormatVersion: 1, ApplicationID: DatabaseApplicationID,
		UserVersion: DatabaseUserVersion, GenerationTwoCompatibility: GenerationTwoCompatibilityDescriptor(),
		OfflineConversions: []OfflineConversionDescriptor{}}
}

func SchemaManifestHash(ctx context.Context, q queryer) (string, error) {
	manifest, err := readGenerationManifest(ctx, q)
	if err != nil {
		return "", err
	}
	return generationManifestDigest(manifest), nil
}
