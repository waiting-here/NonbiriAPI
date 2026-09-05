package charityrouting

import (
	"context"
	"database/sql"
	"fmt"
	"io"
)

const (
	RouteOrdered        = "ordered"
	RouteRandom         = "random"
	RouteExpiryWeighted = "expiry_weighted"
)

func validRouteStrategy(strategy string) bool {
	return strategy == RouteOrdered || strategy == RouteRandom || strategy == RouteExpiryWeighted
}

func defaultRouteStrategy(strategy string) string {
	if strategy == "" {
		return RouteExpiryWeighted
	}
	return strategy
}

func setRoutingStrategy(ctx context.Context, tx *sql.Tx, modelID int64, strategy string) error {
	if !validRouteStrategy(strategy) {
		return ErrInvalidRequest
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO charity_model_routing(model_id,strategy) VALUES(?,?) ON CONFLICT(model_id) DO UPDATE SET strategy=excluded.strategy`, modelID, strategy); err != nil {
		return fmt.Errorf("charity routing: save strategy: %w", err)
	}
	return nil
}

func readRoutingStrategy(ctx context.Context, tx *sql.Tx, modelID int64) (string, error) {
	var strategy string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT strategy FROM charity_model_routing WHERE model_id=?),'expiry_weighted')`, modelID).Scan(&strategy)
	if err != nil {
		return "", fmt.Errorf("charity routing: read strategy: %w", err)
	}
	if !validRouteStrategy(strategy) {
		return "", ErrInvariant
	}
	return strategy, nil
}

func orderRuntimeCandidates(source io.Reader, candidates []weightedRuntimeCandidate, strategy string) ([]RuntimeCandidate, error) {
	if len(candidates) > MaxRuntimeCandidates {
		return nil, ErrResourceLimit
	}
	switch strategy {
	case RouteOrdered:
		result := make([]RuntimeCandidate, len(candidates))
		for index, entry := range candidates {
			result[index] = entry.candidate
		}
		return result, nil
	case RouteRandom:
		uniform := append([]weightedRuntimeCandidate(nil), candidates...)
		for index := range uniform {
			uniform[index].weight = 1
		}
		return orderWeightedRuntimeCandidates(source, uniform)
	case RouteExpiryWeighted:
		return orderWeightedRuntimeCandidates(source, candidates)
	default:
		return nil, ErrInvariant
	}
}
