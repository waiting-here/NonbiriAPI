package db

import "context"

// GenerationTwoCompatibility describes the exact schema accepted by this binary.
type GenerationTwoCompatibility struct {
	SchemaHash           string   `json:"schema_hash"`
	ManifestHash         string   `json:"manifest_hash"`
	SchemaVersion        int      `json:"schema_version"`
	MinimumSchemaVersion int      `json:"minimum_schema_version"`
	SourceManifestHashes []string `json:"source_manifest_hashes"`
	SourceSchemaVersions []int    `json:"source_schema_versions"`
}

const CurrentSchemaVersion = 21
const MinimumSchemaVersion = CurrentSchemaVersion

func GenerationTwoCompatibilityDescriptor() GenerationTwoCompatibility {
	return GenerationTwoCompatibility{
		SchemaHash: PinnedGenerationTwoSchemaHash, ManifestHash: PinnedGenerationTwoManifestHash,
		SchemaVersion: CurrentSchemaVersion, MinimumSchemaVersion: CurrentSchemaVersion,
		SourceManifestHashes: []string{}, SourceSchemaVersions: []int{},
	}
}

func readSchemaVersion(ctx context.Context, q queryer) (int, error) {
	var version int
	err := q.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&version)
	return version, err
}
