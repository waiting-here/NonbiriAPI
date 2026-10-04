package db

import (
	"context"
	"database/sql"
)

func seedLimitedActivities(ctx context.Context, tx *sql.Tx, at int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO limited_activity_configs VALUES('picture-book',0,NULL,NULL,0,?,1,?);
 INSERT INTO limited_activity_revisions VALUES('picture-book',1,0,NULL,NULL,0,?,NULL,?);
 INSERT INTO activity_exchange_state VALUES('picture-book','sketch_paper',X'00000000000000000000000000000000',NULL,1);
 INSERT INTO activity_exchange_state VALUES('picture-book','sketch_brush',X'00000000000000000000000000000000',X'0000000000000000000000000000000A',1);`,
		`{"paper_price":"1000","brush_price":"10000","brush_cap":"10"}`, at,
		`{"paper_price":"1000","brush_price":"10000","brush_cap":"10"}`, at)
	return err
}
