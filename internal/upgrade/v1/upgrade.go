package v1

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const stableSchema17Manifest = "7289b13cd57bb2d6a50a31ffde33cae17ceac2e1e130e0cd759dd2f0c2a98d4b"
const stableSchema18Manifest = "86c1689f93835a22c90c22234906433abe438b7883db41916b01f6553d6ded23"

const stableSchema19Manifest = "a44f84d8610b1141c53015e92cddce874603c2b0512d75528e403b46ff1123a1"

const stableSchema20Manifest = "bb61658c873f8f3a300330f44abed6509f6d0d146ab6588175c4004b9cb39c71"

const stableSchema21Manifest = "56039de6b1c224d85eac5639b9e8d41bbac00136ccb0564514369a863160e7b9"

const stableSchema22Manifest = "0b4eb3ccafe3ae94ff4eae1b45ad1c054d1a7a8037d8aad14ceec38872c0db3a"

const stableSchema23Manifest = "0d4b8dfe2106df2d5688d3a13e6ef2f419825da711e5914fef712c1d93c9c6ea"

const stableSchema24Manifest = "ee3bcf270ed884f80a1ddf2c0cc75a509c20aa3d06c0e46f993beaf1a6b24f46"

const stableSchema25Manifest = "ef68dc29f32b24ac5b06caa8bb6563be24dfae13a1e752385331c9828317cdd9"

const stableSchema26Manifest = "6bcc4ea8fa31bee46e25b5e33b96a3caca18c5c5ff1f257d26b2e2d7acc64b9e"

const stableSchema27Manifest = "5fb9f478d3482547174e43db7f6169621f8bf3d19b706e27e65f03809b002b89"

var storageSchema = schemaRegistry{baseline: 17, target: db.CurrentSchemaVersion, versions: []schemaMigration{
	{version: 17, manifest: stableSchema17Manifest},
	{from: 17, version: 18, manifest: stableSchema18Manifest, apply: extendContentAndProtocols, impact: &migrationImpact{
		rebuiltTables: []string{"mainstream_channels", "endpoints", "endpoint_key_secrets", "donation_keys", "request_attempts", "report_cases", "report_targets", "models", "charity_models", "logical_requests", "request_logs", "game_fishing_batches", "game_fishing_outcome_lengths", "game_fishing_best_lengths", "game_fishing_length_facts"},
		controlTables: []string{"site_config"}, credentials: credentialsRebuilt}},
	{from: 18, version: 19, manifest: stableSchema19Manifest, sql: accountMFASchema, impact: &migrationImpact{
		addedTables:   []string{"account_mfa_credentials", "account_mfa_pending", "account_mfa_attempts", "account_mfa_proofs"},
		controlTables: []string{"site_config"}, constraintTables: []string{"users"}, credentials: credentialsUnchanged}},
	{from: 19, version: 20, manifest: stableSchema20Manifest, sql: logQueryIndexes, impact: &migrationImpact{
		indexedTables: []string{"request_attempts", "request_logs"}, credentials: credentialsUnchanged}},
	{from: 20, version: 21, manifest: stableSchema21Manifest, apply: extendServiceMetadata, impact: &migrationImpact{
		rebuiltTables: []string{"admin_endpoint_tags", "model_discovery_evidence"}, credentials: credentialsUnchanged}},
	{from: 21, version: 22, manifest: stableSchema22Manifest, sql: keyCooldownSchema, impact: &migrationImpact{
		addedTables:      []string{"donation_key_cooldowns", "dispatch_retry_after_observations"},
		constraintTables: []string{"donation_usage_reservations", "dispatch_claims"}, credentials: credentialsUnchanged}},
	{from: 22, version: 23, manifest: stableSchema23Manifest, sql: headerDiagnosticsSchema, impact: &migrationImpact{
		addedTables: []string{"request_header_facts"}, controlTables: []string{"observability_state", "site_config"}, credentials: credentialsUnchanged}},
	{from: 23, version: 24, manifest: stableSchema24Manifest, sql: pricingHistorySchema, impact: &migrationImpact{
		addedTables: []string{"charity_price_templates", "charity_price_template_audits", "donation_owner_preferences"}, credentials: credentialsUnchanged}},
	{from: 24, version: 25, manifest: stableSchema25Manifest, sql: smartBackoffSchema, impact: &migrationImpact{
		addedTables:      []string{"donation_key_backoffs", "dispatch_smart_backoff_facts"},
		constraintTables: []string{"donation_usage_reservations", "dispatch_claims"}, credentials: credentialsUnchanged}},
	{from: 25, version: 26, manifest: stableSchema26Manifest, sql: channelBindingPresetsSchema + automaticBackoffSchema + recurringDiscountSchema, impact: &migrationImpact{
		addedTables:   []string{"channel_charity_binding_presets", "channel_charity_binding_mappings"},
		controlTables: []string{"charity_models", "charity_price_templates"},
		rebuiltTables: []string{"donation_key_backoffs", "dispatch_smart_backoff_facts"}, credentials: credentialsUnchanged}},
	{from: 26, version: 27, manifest: stableSchema27Manifest, sql: endpointBackoffSchema + modelCloudflareSchema, impact: &migrationImpact{
		addedTables: []string{"endpoint_backoff_policies"}, controlTables: []string{"dispatch_smart_backoff_facts", "models", "charity_models"}, credentials: credentialsUnchanged}},
	{from: 27, version: 28, manifest: db.PinnedGenerationTwoManifestHash, apply: extendDirectInference, impact: &migrationImpact{
		rebuiltTables:    []string{"mainstream_channels", "endpoints", "endpoint_key_secrets", "donation_keys", "request_attempts", "report_cases", "report_targets", "models", "charity_models", "logical_requests", "request_logs", "charity_reservations"},
		constraintTables: []string{"donation_usage_reservations"}, credentials: credentialsRebuilt}},
}}

type Options struct {
	Apply               bool
	LegacyGatewayModels string
	Progress            func(Progress)
}

type Result struct {
	Status              string `json:"status"`
	SourceSchemaVersion int    `json:"source_schema_version"`
	TargetSchemaVersion int    `json:"target_schema_version"`
}

func SchemaDescriptor() db.StorageSchemaDescriptor {
	descriptor := db.SchemaDescriptor()
	for _, source := range []struct {
		version      int
		hash, commit string
	}{
		{16, SourceManifestHash, SourceCommit},
		{17, stableSchema17Manifest, "117c0cb009ab285451a3e0238ab97b383faebde3"},
		{18, stableSchema18Manifest, "0f02e60704934b589b51ca91eb34bac9c5e77c02"},
		{19, stableSchema19Manifest, "c4145513b1ca5bd20f0236763c247b8bd5c66df2"},
		{20, stableSchema20Manifest, "51a1e6156cb164802b22fdd881a4ac2040dd1904"},
		{21, stableSchema21Manifest, "bd49471c3e4da5a94bd85292805b17b318f50ddd"},
		{22, stableSchema22Manifest, "ffe31ab3918ec96151b62533fd1ead4c22c466bd"},
		{23, stableSchema23Manifest, "75044b0e685e0f2c3a0d41b19be6e54fd54d7b5b"},
		{24, stableSchema24Manifest, "6f4ee67206eb6fb3a3f345ba06b2d85aba5abc83"},
		{25, stableSchema25Manifest, "2b4f4f62ed5b01c247fcc3f79005358ef0237933"},
		{26, stableSchema26Manifest, "361cb204e888de51901eecc993d3d52b1d156d3a"},
		{27, stableSchema27Manifest, "0fa834ba935649cdf7a141800b5f733d9aaaaf89"},
	} {
		descriptor.OfflineConversions = append(descriptor.OfflineConversions, db.OfflineConversionDescriptor{
			Tool: "nonbiri-upgrade-v1", SourceCommit: source.commit, SourceSchemaVersion: source.version,
			SourceManifestHash: source.hash, TargetSchemaVersion: db.CurrentSchemaVersion})
	}
	return descriptor
}

func Run(ctx context.Context, path string, vault *secret.Vault, options Options) (Result, error) {
	return run(ctx, path, vault, options, false)
}

func RunRC7(ctx context.Context, path string, vault *secret.Vault, options Options) (Result, error) {
	return run(ctx, path, vault, options, true)
}

func run(ctx context.Context, path string, vault *secret.Vault, options Options, rc7Only bool) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("offline conversion context is required")
	}
	ctx, stopProgress := withProgress(ctx, db.CurrentSchemaVersion, options.Progress, 5*time.Second)
	defer stopProgress()
	sources := []OfflineSchema{{Version: 16, ManifestHash: SourceManifestHash}}
	if !rc7Only {
		sources = append(sources, OfflineSchema{Version: 17, ManifestHash: stableSchema17Manifest}, OfflineSchema{Version: 18, ManifestHash: stableSchema18Manifest}, OfflineSchema{Version: 19, ManifestHash: stableSchema19Manifest}, OfflineSchema{Version: 20, ManifestHash: stableSchema20Manifest}, OfflineSchema{Version: 21, ManifestHash: stableSchema21Manifest}, OfflineSchema{Version: 22, ManifestHash: stableSchema22Manifest}, OfflineSchema{Version: 23, ManifestHash: stableSchema23Manifest}, OfflineSchema{Version: 24, ManifestHash: stableSchema24Manifest}, OfflineSchema{Version: 25, ManifestHash: stableSchema25Manifest}, OfflineSchema{Version: 26, ManifestHash: stableSchema26Manifest}, OfflineSchema{Version: 27, ManifestHash: stableSchema27Manifest})
	}
	sourceVersion := db.CurrentSchemaVersion
	invalidLegacyConfiguration := false
	needed, err := convertOffline(ctx, path, vault, sources, options.Apply, true, func(ctx context.Context, database *sql.DB) error {
		if err := database.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&sourceVersion); err != nil {
			return err
		}
		if sourceVersion != 16 {
			return nil
		}
		var initialized int
		if err := database.QueryRowContext(ctx, "SELECT count(*) FROM gateway_model_capabilities_state WHERE singleton=1").Scan(&initialized); err != nil {
			return err
		}
		if initialized == 0 && strings.TrimSpace(options.LegacyGatewayModels) != "" {
			_, err := gatewaypolicy.Parse(options.LegacyGatewayModels)
			invalidLegacyConfiguration = err != nil
			return err
		}
		return nil
	}, func(ctx context.Context, database *sql.DB, tx *sql.Tx) error {
		version, err := readSchemaVersion(ctx, tx)
		if err != nil {
			return err
		}
		if version == 16 {
			return convert(ctx, database, tx, vault, options.LegacyGatewayModels)
		}
		return nil
	})
	if err != nil {
		if invalidLegacyConfiguration {
			return Result{}, errors.New("legacy Gateway model configuration is invalid")
		}
		return Result{}, err
	}
	status := "current"
	if needed {
		status = "ready"
		if options.Apply {
			status = "converted"
		}
	}
	return Result{Status: status, SourceSchemaVersion: sourceVersion, TargetSchemaVersion: db.CurrentSchemaVersion}, nil
}
