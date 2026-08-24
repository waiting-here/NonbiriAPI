package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type GameSettlementExportRow struct {
	ID           string `json:"id"`
	GameType     string `json:"game_type"`
	GameVersion  int    `json:"game_version"`
	State        string `json:"state"`
	Entry        string `json:"entry"`
	Payout       string `json:"payout"`
	CreatedAt    int64  `json:"created_at"`
	AutoSettleAt int64  `json:"auto_settle_at"`
	FinalizedAt  *int64 `json:"finalized_at"`
}

type GameRoundExportRow struct {
	ID            string `json:"id"`
	GameType      string `json:"game_type"`
	GameVersion   int    `json:"game_version"`
	SettlementID  string `json:"settlement_id"`
	EventSequence int64  `json:"event_seq"`
	CreatedAt     int64  `json:"created_at"`
	SettledAt     *int64 `json:"settled_at"`
	RevealedAt    *int64 `json:"revealed_at"`
}

type FishingOutcomeExportRow struct {
	RoundID    string `json:"round_id"`
	Bait       string `json:"bait"`
	SpeciesKey string `json:"species_key"`
	Tier       string `json:"tier"`
	SizeCM     int    `json:"size_cm"`
	IsJunk     bool   `json:"is_junk"`
	IsTreasure bool   `json:"is_treasure"`
	Meter      bool   `json:"meter"`
	Price      string `json:"price"`
	CreditsWon string `json:"credits_won"`
}

type FishingBestExportRow struct {
	RoundID    *string `json:"round_id"`
	SpeciesKey string  `json:"species_key"`
	Tier       string  `json:"tier"`
	SizeCM     int     `json:"size_cm"`
	CaughtAt   int64   `json:"caught_at"`
}

func (s *Store) ListExportGameSettlements(ctx context.Context, userID int64, limit int) ([]GameSettlementExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,game_type,game_version,state,entry_milli,payout_milli,created_at,auto_settle_at,finalized_at
FROM game_settlements WHERE user_id=? ORDER BY created_at,id LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export game settlements: %w", err)
	}
	defer rows.Close()
	out := make([]GameSettlementExportRow, 0, min(limit, 32))
	for rows.Next() {
		var row GameSettlementExportRow
		var entry, payout int64
		var finalized sql.NullInt64
		if err := rows.Scan(&row.ID, &row.GameType, &row.GameVersion, &row.State, &entry, &payout, &row.CreatedAt, &row.AutoSettleAt, &finalized); err != nil {
			return nil, fmt.Errorf("export game settlements scan: %w", err)
		}
		row.Entry, row.Payout = credits.FormatAmount(entry), credits.FormatAmount(payout)
		row.FinalizedAt = nullInt64Ptr(finalized)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export game settlements rows: %w", err)
	}
	if len(out) > limit {
		return nil, ErrExportLimit
	}
	return out, nil
}

func (s *Store) ListExportGameRounds(ctx context.Context, userID int64, limit int) ([]GameRoundExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,game_type,game_version,settlement_id,event_seq,created_at,settled_at,revealed_at
FROM game_rounds WHERE user_id=? ORDER BY created_at,id LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export game rounds: %w", err)
	}
	defer rows.Close()
	out := make([]GameRoundExportRow, 0, min(limit, 32))
	for rows.Next() {
		var row GameRoundExportRow
		var settled, revealed sql.NullInt64
		if err := rows.Scan(&row.ID, &row.GameType, &row.GameVersion, &row.SettlementID, &row.EventSequence, &row.CreatedAt, &settled, &revealed); err != nil {
			return nil, fmt.Errorf("export game rounds scan: %w", err)
		}
		row.SettledAt, row.RevealedAt = nullInt64Ptr(settled), nullInt64Ptr(revealed)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export game rounds rows: %w", err)
	}
	if len(out) > limit {
		return nil, ErrExportLimit
	}
	return out, nil
}

func (s *Store) ListExportFishingOutcomes(ctx context.Context, userID int64, limit int) ([]FishingOutcomeExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT r.id,o.bait,o.species_key,o.tier,o.size_cm,s.entry_milli,s.payout_milli
FROM game_rounds r JOIN game_fishing_outcomes o ON o.round_id=r.id
JOIN game_settlements s ON s.id=r.settlement_id
WHERE r.user_id=? AND r.game_type=? AND r.game_version=?
ORDER BY r.created_at,r.id LIMIT ?`, userID, game.FishingID, game.FishingVersion, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export fishing outcomes: %w", err)
	}
	defer rows.Close()
	out := make([]FishingOutcomeExportRow, 0, min(limit, 32))
	for rows.Next() {
		var row FishingOutcomeExportRow
		var entry, payout int64
		if err := rows.Scan(&row.RoundID, &row.Bait, &row.SpeciesKey, &row.Tier, &row.SizeCM, &entry, &payout); err != nil {
			return nil, fmt.Errorf("export fishing outcomes scan: %w", err)
		}
		row.IsJunk, row.IsTreasure = row.Tier == "junk", row.Tier == "treasure"
		row.Meter = !row.IsJunk && !row.IsTreasure && row.SizeCM >= 100
		row.Price, row.CreditsWon = credits.FormatAmount(entry), credits.FormatAmount(payout)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export fishing outcomes rows: %w", err)
	}
	if len(out) > limit {
		return nil, ErrExportLimit
	}
	return out, nil
}

func (s *Store) ListExportFishingBest(ctx context.Context, userID int64, limit int) ([]FishingBestExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT round_id,species_key,tier,size_cm,caught_at
FROM game_fishing_best WHERE user_id=? ORDER BY user_id LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export fishing best: %w", err)
	}
	defer rows.Close()
	out := make([]FishingBestExportRow, 0, min(limit, 4))
	for rows.Next() {
		var row FishingBestExportRow
		var roundID sql.NullString
		if err := rows.Scan(&roundID, &row.SpeciesKey, &row.Tier, &row.SizeCM, &row.CaughtAt); err != nil {
			return nil, fmt.Errorf("export fishing best scan: %w", err)
		}
		if roundID.Valid {
			value := roundID.String
			row.RoundID = &value
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export fishing best rows: %w", err)
	}
	if len(out) > limit {
		return nil, ErrExportLimit
	}
	return out, nil
}
