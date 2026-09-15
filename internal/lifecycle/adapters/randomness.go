package adapters

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type RandomnessAdapter struct{}

func (RandomnessAdapter) ExportRandomness(ctx context.Context, tx *sql.Tx, r lifecycle.ExportRequest) ([]lifecycle.RandomnessProofExport, error) {
	proofs, err := randomness.ExportForUser(ctx, tx, r.UserID, r.DecisionNow, r.Limit, lifecycle.MaxExportBytes)
	if errors.Is(err, randomness.ErrLimit) {
		return nil, lifecycle.ErrTooLarge
	}
	if err != nil {
		return nil, err
	}
	result := make([]lifecycle.RandomnessProofExport, len(proofs))
	for i, p := range proofs {
		out := lifecycle.RandomnessProofExport{Algorithm: p.Algorithm, Game: p.Game, ResourceID: p.ResourceID, Rules: p.Rules, Commitment: p.Commitment, Seed: p.Seed}
		for _, stream := range p.Streams {
			out.Streams = append(out.Streams, lifecycle.RandomnessStreamExport{Label: stream.Label, Samples: stream.Samples})
		}
		result[i] = out
	}
	return result, nil
}
