package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Only the complete published source is eligible for this additive extension.
const preInteractionManifestHash = "66319db6ffcd94d214725a7f21b3b10b8efb9bdc055d17acc14579cde6c86009"

var interactionChangedTables = []string{"charity_model_routing", "game_rank_totals", "game_rank_net_rebuild_totals", "credit_operations", "discord_blacklist"}

func interactionTableSQL(table, previous string) (string, error) {
	var changes [][2]string
	switch table {
	case "discord_blacklist":
		changes = append(changes, [2]string{"length(reason) BETWEEN 1 AND 1024", "length(reason) BETWEEN 1 AND 2000"})
	case "charity_model_routing":
		changes = append(changes, [2]string{"'ordered','random','expiry_weighted'", "'ordered','random','expiry_weighted','cache_balanced'"})
	case "game_rank_totals":
		// Every occurrence is a board identity or its matching window/sign rule.
		if strings.Count(previous, "'blackjack_net_profit'") != 3 {
			return "", errors.New("unrecognized game ranking constraints")
		}
		previous = strings.ReplaceAll(previous, "'blackjack_net_profit'", "'blackjack_net_profit','bidding_net_profit'")
	case "game_rank_net_rebuild_totals":
		changes = append(changes, [2]string{"'blackjack_net_profit'", "'blackjack_net_profit','bidding_net_profit'"})
	case "credit_operations":
		// These activity operations use the existing operation identity and
		// immutable posting path; domain receipts bind the challenge or node.
		if strings.Count(previous, "'activity_exchange','inactivity_decay'") != 2 {
			return "", errors.New("unrecognized activity ledger constraints")
		}
		previous = strings.ReplaceAll(previous, "'activity_exchange','inactivity_decay'", "'activity_exchange','inactivity_decay','fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund'")
	default:
		return "", errors.New("unknown interaction extension table")
	}
	for _, change := range changes {
		if strings.Count(previous, change[0]) != 1 {
			return "", errors.New("unrecognized interaction source constraint")
		}
		previous = strings.Replace(previous, change[0], change[1], 1)
	}
	return previous, nil
}

func interactionAdditiveSchema() string {
	return routingPolicySchema + accountContinuitySchema + imageCapabilitySchema + imageCapabilityGuardsSchema() + auditProjectionSchema + fatFishSchema
}

func interactionBootstrapSchema(previous string) string {
	for _, table := range interactionChangedTables {
		start := "CREATE TABLE " + table + " ("
		_, tail, ok := strings.Cut(previous, start)
		if !ok {
			panic("missing canonical interaction table: " + table)
		}
		body, _, ok := strings.Cut(tail, ";")
		if !ok {
			panic("incomplete canonical interaction table")
		}
		old := start + body
		target, err := interactionTableSQL(table, old)
		if err != nil || strings.Count(previous, old) != 1 {
			panic("invalid canonical interaction table: " + table)
		}
		previous = strings.Replace(previous, old, target, 1)
	}
	return previous + interactionAdditiveSchema()
}

func applyInteractionExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preInteractionManifestHash {
		return errors.New("unrecognized interaction source manifest")
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("schema version cannot advance")
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	for _, table := range interactionChangedTables {
		var before string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&before); err != nil {
			return err
		}
		after, err := interactionTableSQL(table, before)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, after, table, before)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return errors.New("interaction schema update count mismatch")
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;", version+1)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, interactionAdditiveSchema()); err != nil {
		return err
	}
	return seedInteractionStorage(ctx, tx)
}

func seedInteractionStorage(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO limited_activity_configs(activity_key,visible,starts_at,ends_at,paused,module_config,revision,updated_at)
 VALUES('fat-fish',0,NULL,NULL,0,'{}',1,0);
INSERT INTO limited_activity_revisions(activity_key,revision,visible,starts_at,ends_at,paused,module_config,actor_user_id,created_at)
 VALUES('fat-fish',1,0,NULL,NULL,0,'{}',NULL,0);
INSERT INTO charity_routing_capacity(id,affinities,buckets,revision) VALUES(1,0,0,1);
INSERT INTO charity_routing_settings(model_id,revision,affinity_ttl_seconds) SELECT id,1,300 FROM charity_models;
INSERT INTO fatfish_capacity(id,active_challenges,summary_rows) VALUES(1,0,0);
INSERT INTO game_bidding_net_rebuild(id,state,watermark,last_seq,history_coverage_start,missing_events,updated_at)
 VALUES(1,'pending',NULL,zeroblob(16),NULL,0,0);
INSERT INTO discord_blacklist_origins(discord_id,first_actor_kind,first_actor_user_id)
 SELECT discord_id,'admin',NULL FROM discord_blacklist;
UPDATE admin_account_deletions SET former_user_id=CASE WHEN json_type(snapshot_json,'$.user_id') IN ('text','integer')
 AND CAST(json_extract(snapshot_json,'$.user_id') AS TEXT) GLOB '[1-9]*'
 AND CAST(json_extract(snapshot_json,'$.user_id') AS TEXT) NOT GLOB '*[^0-9]*'
 AND (length(CAST(json_extract(snapshot_json,'$.user_id') AS TEXT))<19 OR
  (length(CAST(json_extract(snapshot_json,'$.user_id') AS TEXT))=19 AND CAST(json_extract(snapshot_json,'$.user_id') AS TEXT)<='9223372036854775807'))
 THEN CAST(json_extract(snapshot_json,'$.user_id') AS INTEGER) END,
 discord_id=CASE WHEN json_type(snapshot_json,'$.discord_id')='text' THEN json_extract(snapshot_json,'$.discord_id') END,
 deleted_at=(SELECT created_at FROM admin_alerts WHERE id=alert_id);
INSERT INTO image_model_capability_revisions(model_id,revision,schema_version,readiness,source_json,manual_json,effective_json,candidate_hash,created_at)
 SELECT model_id,revision,1,'legacy','{}',
 json_object('parameters',json(parameters_json),'combinations',json(combinations_json),'mapping',json(mapping_json)),
 json_object('parameters',json(parameters_json),'combinations',json(combinations_json),'mapping',json(mapping_json)),NULL,created_at FROM image_model_revisions;
INSERT INTO image_model_pricing_revisions(model_id,revision,default_paper_mag,default_brush_mag,fallback,tiers_json,sizes_json,created_at)
 SELECT model_id,revision,paper_price_mag,brush_price_mag,'default','[]','[]',created_at FROM image_model_revisions;
INSERT INTO image_model_revision_policies(model_id,model_revision,capability_revision,pricing_revision)
 SELECT model_id,revision,revision,revision FROM image_model_revisions;
INSERT INTO image_task_price_receipts(task_id,user_id,model_revision,pricing_revision,n,unit_paper_mag,unit_brush_mag,total_paper_mag,total_brush_mag,basis,price_key,created_at)
 SELECT t.id,t.user_id,t.model_revision,t.model_revision,t.n,m.paper_price_mag,m.brush_price_mag,t.paper_charge_mag,t.brush_charge_mag,'legacy','',t.created_at
 FROM image_activity_tasks t JOIN image_model_revisions m ON m.model_id=t.model_id AND m.revision=t.model_revision
 WHERE t.user_id IS NOT NULL;`)
	return err
}
