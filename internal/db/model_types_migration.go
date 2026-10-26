package db

import (
	"context"
	"database/sql"
)

type modelTypesMigrationDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// UpgradeModelTypes preserves the shared Chat/Responses meaning of source
// schemas through 36. It runs only in the upgrade transaction; current models
// retain independently selected interfaces and their original revisions.
func UpgradeModelTypes(ctx context.Context, q modelTypesMigrationDB) error {
	for _, table := range []string{"models", "charity_models"} {
		if _, err := q.ExecContext(ctx, "UPDATE "+table+" SET model_types=model_types|17 WHERE (model_types&17) IN (1,16)"); err != nil {
			return err
		}
	}
	return nil
}
