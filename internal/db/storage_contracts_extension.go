package db

import (
	"context"
	"database/sql"
)

func seedLakeNotesStorage(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO limited_activity_configs(activity_key,visible,starts_at,ends_at,paused,module_config,revision,updated_at)
 VALUES('lake-notes',0,NULL,NULL,0,'{}',1,0);
INSERT INTO limited_activity_revisions(activity_key,revision,visible,starts_at,ends_at,paused,module_config,actor_user_id,created_at)
 VALUES('lake-notes',1,0,NULL,NULL,0,'{}',NULL,0);`)
	return err
}
