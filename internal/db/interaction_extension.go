package db

import (
	"context"
	"database/sql"
)

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
