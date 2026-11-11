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

const CurrentSchemaVersion = 54

const Schema53ManifestHash = "1f14811104ce6a18a8c2b5bdf46b2bf9566074e1efbdf4e53acf4f588ec06b63"

const Schema52ManifestHash = "90043bbe2d1e64dc903649766e30ca1f852139f251a3ef4f4955d7d7cf5a7d87"

const Schema51ManifestHash = "130ebd0797b2c9b7297cc7ac9c0358545db10dce4ed440a3b3aadce9564d52c8"

const Schema50ManifestHash = "243a0cf02430a3448f5fa132036d6833d29a130d530c183039744ea8b3bbfac4"
const MinimumSchemaVersion = CurrentSchemaVersion

const Schema49ManifestHash = "597d1fda2ed07cdf576add0e141a7640d934de3afeb6feb76212d7861890749c"

const Schema48ManifestHash = "c2246b02766bc86b49e79d5730d136ed7d2506e6923c43b0bfdd35a9eb71d534"

const Schema47ManifestHash = "6b472d22be769e1b7c825b0a3e5424aac9e647e9754c3ec113a31dae148b2eb5"

const Schema46ManifestHash = "89e58fc77c8a252f0b68cec0398f9b30fd52a97a0ec1e52b78ce461c9d2df46d"

const Schema45ManifestHash = "84aac032c0e2ea8ac1b574f959590ab7d782daae0c9696f479331c75996d2fa9"

const Schema44ManifestHash = "34f60341e1c5cfbff150726855fc5e2b9793b9cb75e6b6217717dd2e8d946ef5"

const Schema43ManifestHash = "db3c16e4e0c6b74cb5e9f83144df94e454aa071a30f3290db80b8795b5265c09"

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
