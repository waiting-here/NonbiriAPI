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
INSERT INTO fatfish_capacity(id,active_challenges,summary_rows) VALUES(1,0,0);
INSERT INTO game_bidding_net_rebuild(id,state,watermark,last_seq,history_coverage_start,missing_events,updated_at)
 VALUES(1,'pending',NULL,zeroblob(16),NULL,0,0);`)
	return err
}
