package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/game/steadycatch"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type CatchAdapter struct{ service *host.Service }

func NewRegisteredCatch(s *host.Service) *CatchAdapter { return &CatchAdapter{s} }
func (a *CatchAdapter) ExportCatch(ctx context.Context, tx *sql.Tx, r lifecycle.ExportRequest) (lifecycle.CatchExport, lifecycle.ExportFinalizer, error) {
	v, end, err := exportRegisteredGame[steadycatch.Export](a.service, game.SteadyCatchID, ctx, tx, r)
	out := lifecycle.CatchExport{FirstCleared: v.FirstCleared, Sessions: []json.RawMessage{}}
	if err != nil {
		return out, end, err
	}
	for _, s := range v.Sessions {
		body, err := json.Marshal(s)
		if err != nil {
			return out, end, err
		}
		out.Sessions = append(out.Sessions, body)
	}
	return out, end, nil
}
func (a *CatchAdapter) PrepareDelete(ctx context.Context, tx *sql.Tx, r lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	return deleteRegisteredGame(a.service, game.SteadyCatchID, ctx, tx, r)
}
func (a *CatchAdapter) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	return retainRegisteredGame(a.service, game.SteadyCatchID, ctx, now, limit, deadline)
}
