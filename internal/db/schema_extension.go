package db

import (
	"context"
)

// GenerationTwoCompatibility describes the exact schema accepted by this binary.
type GenerationTwoCompatibility struct {
	SchemaHash           string   `json:"schema_hash"`
	ManifestHash         string   `json:"manifest_hash"`
	SchemaVersion        int      `json:"schema_version"`
	MinimumSchemaVersion int      `json:"minimum_schema_version"`
	SourceManifestHashes []string `json:"source_manifest_hashes"`
	SourceSchemaVersions []int    `json:"source_schema_versions"`
}

const CurrentSchemaVersion = 43
const MinimumSchemaVersion = CurrentSchemaVersion

const Schema42ManifestHash = "7118778b4ef5db7a41f899406a647d6cb3a2676ad86cf1de6d1fc8d4c6ee20ec"

const Schema41ManifestHash = "88ecbe32e8e4d40bbc301ae58916d4597c686366588f3402aad2f442ebb57236"

const Schema40ManifestHash = "148c5472e40a1d771604d6b74077c7a3e635f20042f5bf935ad62ee158acc8f7"

const Schema39ManifestHash = "cc2091dfa70999fb270f2498f99b2be5f0bc555d7ef3fae063c461b36338bb38"

const Schema38ManifestHash = "ac15d08c2527e8b6a1f82f75cf72cc41fdb2fb0c4ba41ded44e39e2f71505099"

const Schema37ManifestHash = "ace58908001ae7f2a32b16d0b0cf7d9c5736c27f1a8983c4844b88157f27fb4d"

const Schema36ManifestHash = "3997a42c32621f190343be2bf5a9268e3640fd696a36343404739f9de06a771a"

// Schema33ManifestHash pins a published source accepted by the offline upgrader.
const Schema33ManifestHash = "bf3821c938fba16a863df866b801621068722c97c57acb67191075cb3c89f550"

const Schema35ManifestHash = "3ddfc2a65af916def3e6ea53545f29e7f5883778ee2b841f45993542359fd108"

const Schema34ManifestHash = "6f5e1dc12a53ccd7dc8cd07a98d09c0f6d9b411aa31caddd97aeb58fc6321571"

// EndpointKeyHistoryIndexesSQL is applied by the offline upgrade chain.
const EndpointKeyHistoryIndexesSQL = `CREATE INDEX idx_request_logs_endpoint_key ON request_logs(endpoint_key_id);
CREATE INDEX idx_dispatch_claims_endpoint_key ON dispatch_claims(endpoint_key_id);
CREATE INDEX idx_report_targets_endpoint_key ON report_targets(endpoint_key_id);
CREATE INDEX idx_donation_keys_endpoint_key ON donation_keys(endpoint_key_id);
`

func GenerationTwoCompatibilityDescriptor() GenerationTwoCompatibility {
	return GenerationTwoCompatibility{
		SchemaHash: PinnedGenerationTwoSchemaHash, ManifestHash: PinnedGenerationTwoManifestHash,
		SchemaVersion: CurrentSchemaVersion, MinimumSchemaVersion: MinimumSchemaVersion,
		SourceManifestHashes: []string{}, SourceSchemaVersions: []int{},
	}
}

func readSchemaVersion(ctx context.Context, q queryer) (int, error) {
	var version int
	err := q.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&version)
	return version, err
}
