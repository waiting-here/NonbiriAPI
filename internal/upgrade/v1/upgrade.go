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
	{from: 26, version: 33, manifest: db.Schema33ManifestHash, apply: extendConnections, impact: connectionsMigrationImpact},
	{from: 33, version: 34, manifest: db.Schema34ManifestHash, sql: db.EndpointKeyHistoryIndexesSQL, impact: &migrationImpact{
		indexedTables: []string{"request_logs", "dispatch_claims", "report_targets", "donation_keys"}, credentials: credentialsUnchanged}},
	{from: 34, version: 35, manifest: db.Schema35ManifestHash, sql: db.DonationDiscoveryAuthorizationSQL, impact: &migrationImpact{
		constraintTables: []string{"discovery_runs", "charity_model_catalog"}, credentials: credentialsUnchanged}},
	{from: 35, version: 36, manifest: db.Schema36ManifestHash, sql: db.StewardPermissionsSQL + db.StewardAuditRolesSQL + db.EndpointTagsSQL, impact: &migrationImpact{rebuiltTables: []string{"risk_client_rules", "client_rule_auto_bans", "discord_blacklist_origins", "discord_blacklist_events", "admin_endpoint_tags"}, addedTables: []string{"steward_permissions", "steward_permission_audits"}, credentials: credentialsUnchanged}},
	{from: 36, version: 37, manifest: db.Schema37ManifestHash, sql: db.AgentRequestsSQL + db.AgentWorkflowSQL, apply: func(ctx context.Context, tx *sql.Tx) error {
		if err := db.UpgradeModelTypes(ctx, tx); err != nil {
			return err
		}
		return db.UpgradeStewardAIPermissions(ctx, tx)
	}, impact: &migrationImpact{controlTables: []string{"steward_permissions", "models", "charity_models"}, addedTables: []string{"ai_agent_plans", "ai_agent_model_results", "ai_agent_review_events", "ai_agent_jobs", "ai_agent_messages", "ai_agent_conversations", "ai_agent_memory_history", "ai_agent_memories", "ai_agent_settings", "agent_request_response_chunks", "agent_request_settings", "agent_requests", "agent_request_usage", "agent_request_usage_totals", "agent_request_usage_hours"}, credentials: credentialsUnchanged}},
	{from: 37, version: 38, manifest: db.Schema38ManifestHash, sql: db.CharityModelDeletionIndexesSQL, impact: &migrationImpact{
		indexedTables: []string{"charity_reservations", "charity_dispatch_receipts", "charity_key_affinities", "charity_header_sessions", "charity_header_uses"}, credentials: credentialsUnchanged}},
	{from: 38, version: 39, manifest: db.PinnedGenerationTwoManifestHash, sql: db.EndpointTagRulesSQL, impact: &migrationImpact{
		rebuiltTables: []string{"admin_endpoint_tags"}, addedTables: []string{"endpoint_tag_rules", "endpoint_tag_rule_tags"}, controlTables: []string{"endpoint_tag_domains"}, indexedTables: []string{"donation_keys"}, constraintTables: []string{"endpoints", "donation_keys"}, credentials: credentialsUnchanged}},
}}

type Options struct {
	Apply               bool
	LegacyGatewayModels string
	Progress            func(Progress)
}

type Result struct {
	Tool                          string `json:"tool"`
	TargetManifestHash            string `json:"target_manifest_hash"`
	SupportedSourceSchemaVersions []int  `json:"supported_source_schema_versions"`
	Status                        string `json:"status"`
	SourceSchemaVersion           int    `json:"source_schema_version"`
	TargetSchemaVersion           int    `json:"target_schema_version"`
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
		{33, db.Schema33ManifestHash, "a7ea261babd424470ff67d1d1b9a119c29437d2e"},
		{34, db.Schema34ManifestHash, "8576d726e49bd76db6d191914cb4f3637b25f493"},
		{35, db.Schema35ManifestHash, "2df56fe8814e6a9968380fc8461942065eed3f12"},
		{36, db.Schema36ManifestHash, "1fc9527f0c9023542dfc7decf59789733a6a834f"},
		{37, db.Schema37ManifestHash, "4413e15527dff68e42386ca4841c38fc93911539"},
		{38, db.Schema38ManifestHash, "de51645924a488d742baad5ff1a35f6569f22746"},
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
		for _, schema := range storageSchema.versions {
			if schema.version < storageSchema.target {
				sources = append(sources, OfflineSchema{Version: schema.version, ManifestHash: schema.manifest})
			}
		}
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
	supported := make([]int, 0, len(sources))
	for _, source := range sources {
		supported = append(supported, source.Version)
	}
	tool := "nonbiri-upgrade-v1"
	if rc7Only {
		tool = "nonbiri-upgrade-rc7"
	}
	return Result{Tool: tool, Status: status, SourceSchemaVersion: sourceVersion, TargetSchemaVersion: db.CurrentSchemaVersion,
		TargetManifestHash: db.PinnedGenerationTwoManifestHash, SupportedSourceSchemaVersions: supported}, nil
}
