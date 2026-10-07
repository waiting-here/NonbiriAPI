package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Each session includes its versioned checkpoint and replayable control batches.
type CatchExport struct {
	FirstCleared bool              `json:"first_cleared"`
	Sessions     []json.RawMessage `json:"sessions"`
}
type CatchExporter interface {
	ExportCatch(context.Context, *sql.Tx, ExportRequest) (CatchExport, ExportFinalizer, error)
}
