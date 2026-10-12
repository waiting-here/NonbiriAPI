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

const RC7SourceCommit = "82f1711b674425745a768f781e0e0c34dcb37ac3"
const RC7SourceSchemaVersion = 16
const RC7SourceManifestHash = "484bf7ee92aec1dfb9c8750e9bb1a73b2b4b5d1e9761e1f86c5dcd31d2719f23"

// SchemaDescriptor reads compiled metadata only; no environment or database
// is needed by deployment tooling.
func SchemaDescriptor() StorageSchemaDescriptor {
	return StorageSchemaDescriptor{FormatVersion: 1, ApplicationID: DatabaseApplicationID,
		UserVersion: DatabaseUserVersion, GenerationTwoCompatibility: GenerationTwoCompatibilityDescriptor(),
		OfflineConversions: []OfflineConversionDescriptor{{Tool: "nonbiri-upgrade-rc7", SourceCommit: RC7SourceCommit, SourceSchemaVersion: RC7SourceSchemaVersion, SourceManifestHash: RC7SourceManifestHash, TargetSchemaVersion: 17}}}
}

func SchemaManifestHash(ctx context.Context, q queryer) (string, error) {
	manifest, err := readGenerationManifest(ctx, q)
	if err != nil {
		return "", err
	}
	return generationManifestDigest(manifest), nil
}
