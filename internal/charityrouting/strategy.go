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
	RouteCacheBalanced  = "cache_balanced"
)

func validRouteStrategy(strategy string) bool {
	return strategy == RouteOrdered || strategy == RouteRandom || strategy == RouteExpiryWeighted || strategy == RouteCacheBalanced
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
			uniform[index].denominator = 1
		}
		return orderWeightedRuntimeCandidates(source, uniform)
	case RouteExpiryWeighted, RouteCacheBalanced:
		return orderWeightedRuntimeCandidates(source, candidates)
	default:
		return nil, ErrInvariant
	}
}

// OrderSelected runs only after request preflight selected one target per
// binding. Adding targets therefore cannot add weight or retry slots.
func (s *Service) OrderSelected(candidates []RuntimeCandidate, strategy string) ([]RuntimeCandidate, error) {
	if s == nil {
		return nil, ErrInvalidRequest
	}
	weighted := make([]weightedRuntimeCandidate, len(candidates))
	seen := make(map[int64]bool, len(candidates))
	for index, candidate := range candidates {
		if candidate.BindingID <= 0 || seen[candidate.BindingID] {
			return nil, ErrInvariant
		}
		seen[candidate.BindingID] = true
		weighted[index] = weightedRuntimeCandidate{candidate: candidate, denominator: candidate.WeightDenominator}
	}
	return orderRuntimeCandidates(s.entropy, weighted, strategy)
}
