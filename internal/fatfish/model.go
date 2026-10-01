package fatfish

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const maximumUnix = int64(253402300799)

type Amounts struct {
	UnlockCost       string    `json:"unlock_cost"`
	TicketPrice      string    `json:"ticket_price"`
	FirstClearReward string    `json:"first_clear_reward"`
	StarRewards      [3]string `json:"star_rewards"`
}

type Progress struct {
	Unlocked       bool   `json:"unlocked"`
	Passed         bool   `json:"passed"`
	BestStars      int    `json:"best_stars"`
	BestScoreUnits string `json:"best_score_units"`
	BestAtMS       *int64 `json:"best_at_ms"`
}

type NodeView struct {
	ID            string          `json:"id"`
	PeriodID      string          `json:"period_id"`
	Title         string          `json:"title,omitempty"`
	Description   string          `json:"description,omitempty"`
	MapX          int             `json:"map_x"`
	MapY          int             `json:"map_y"`
	Order         int             `json:"order"`
	Hidden        bool            `json:"hidden"`
	Eligible      bool            `json:"eligible"`
	Progress      Progress        `json:"progress"`
	Condition     json.RawMessage `json:"condition,omitempty"`
	ConditionHint *ConditionHint  `json:"condition_hint,omitempty"`
	Revision      string          `json:"revision,omitempty"`
	VersionID     string          `json:"version_id,omitempty"`
	ContentHash   string          `json:"content_hash,omitempty"`
	Amounts       *Amounts        `json:"amounts,omitempty"`
	Level         json.RawMessage `json:"level,omitempty"`
}

type PeriodView struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	State            string     `json:"state"`
	Visible          bool       `json:"visible"`
	Paused           bool       `json:"paused"`
	PastPublic       bool       `json:"past_public"`
	StartsAt         int64      `json:"starts_at"`
	EndsAt           int64      `json:"ends_at"`
	Revision         string     `json:"revision"`
	LeaderboardFinal bool       `json:"leaderboard_final"`
	Nodes            []NodeView `json:"nodes,omitempty"`
}

type PeriodPage struct {
	Items    []PeriodView `json:"items"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	HasMore  bool         `json:"has_more"`
}

type ChallengeView struct {
	Revision       string          `json:"revision"`
	ID             string          `json:"id"`
	State          string          `json:"state"`
	PeriodID       string          `json:"period_id,omitempty"`
	NodeID         string          `json:"node_id,omitempty"`
	VersionID      string          `json:"version_id"`
	NodeRevision   string          `json:"node_revision,omitempty"`
	ContentHash    string          `json:"content_hash"`
	EngineVersion  int             `json:"engine_version"`
	ScoringVersion int             `json:"scoring_version"`
	SeedCommit     string          `json:"seed_commit"`
	Seed           string          `json:"seed,omitempty"`
	Level          json.RawMessage `json:"level,omitempty"`
	PreparedAtMS   int64           `json:"prepared_at_ms"`
	PrepareUntilMS int64           `json:"prepare_until_ms"`
	StartAtMS      *int64          `json:"start_at_ms"`
	EndAtMS        *int64          `json:"end_at_ms"`
	SubmitUntilMS  *int64          `json:"submit_until_ms"`
	ServerNowMS    int64           `json:"server_now_ms"`
	TicketPrice    string          `json:"ticket_price"`
	Result         *ResultView     `json:"result,omitempty"`
}

type HistoryPage struct {
	Items    []ChallengeView `json:"items"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	HasMore  bool            `json:"has_more"`
}

type ResultView struct {
	State                  string             `json:"state"`
	Reason                 string             `json:"reason"`
	TerminalTick           int                `json:"terminal_tick"`
	Fed                    int                `json:"fed"`
	Total                  int                `json:"total"`
	BowlCounts             []engine.BowlState `json:"bowl_counts"`
	Passed                 bool               `json:"passed"`
	Stars                  int                `json:"stars"`
	ScoreUnits             string             `json:"score_units"`
	EngineVersion          int                `json:"engine_version"`
	ScoringVersion         int                `json:"scoring_version"`
	ContentHash            string             `json:"content_hash"`
	FinalStateHash         string             `json:"final_state_hash"`
	SeedCommit             string             `json:"seed_commit"`
	CommitmentVerified     bool               `json:"commitment_verified"`
	TicketCharge           string             `json:"ticket_charge"`
	TicketRefund           string             `json:"ticket_refund"`
	Rewards                string             `json:"rewards"`
	VerificationDurationMS int64              `json:"verification_duration_ms,omitempty"`
}

func (s *Service) nowMS() (int64, error) {
	if s == nil || s.now == nil {
		return 0, ErrInvalid
	}
	ms := s.now().UTC().UnixMilli()
	if ms < 0 || ms > maximumUnix*1000 {
		return 0, ErrInvalid
	}
	return ms, nil
}

func parseRevision(text string) (int64, error) {
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < 1 || n == int64(^uint64(0)>>1) || strconv.FormatInt(n, 10) != text {
		return 0, ErrInvalid
	}
	return n, nil
}

func parseAmount(text string) (db.U128, error) {
	parts := strings.Split(text, ".")
	if len(parts) == 0 || len(parts) > 2 || parts[0] == "" || len(parts[0]) > 1 && parts[0][0] == '0' {
		return db.U128{}, ErrInvalid
	}
	for _, ch := range parts[0] {
		if ch < '0' || ch > '9' {
			return db.U128{}, ErrInvalid
		}
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
		if len(frac) < 1 || len(frac) > 3 || frac[len(frac)-1] == '0' {
			return db.U128{}, ErrInvalid
		}
		for _, ch := range frac {
			if ch < '0' || ch > '9' {
				return db.U128{}, ErrInvalid
			}
		}
	}
	value, ok := new(big.Int).SetString(parts[0]+frac+strings.Repeat("0", 3-len(frac)), 10)
	if !ok || value.BitLen() > 127 {
		return db.U128{}, ErrInvalid
	}
	return db.U128FromBig(value)
}

func amountText(raw []byte) (string, error) {
	u, err := db.DecodeU128(raw)
	if err != nil {
		return "", ErrInvariant
	}
	q, r := new(big.Int).QuoRem(u.Big(), big.NewInt(1000), new(big.Int))
	if r.Sign() == 0 {
		return q.String(), nil
	}
	frac := strconv.FormatInt(r.Int64()+1000, 10)[1:]
	return q.String() + "." + strings.TrimRight(frac, "0"), nil
}

func amountFromMag(raw []byte) (ledger.Amount, error) {
	u, err := db.DecodeU128(raw)
	if err != nil {
		return ledger.Amount{}, ErrInvariant
	}
	return ledger.AmountFromBig(u.Big())
}

func sameCapability(saved []byte, rawHex string) bool {
	raw, err := hex.DecodeString(rawHex)
	if err != nil || len(raw) != 32 || strings.ToLower(rawHex) != rawHex {
		return false
	}
	h := sha256.Sum256(raw)
	return len(saved) == len(h) && subtle.ConstantTimeCompare(saved, h[:]) == 1
}

func (s *Service) authorizeUserTx(ctx context.Context, tx *sql.Tx, userID, nowMS int64, newWork bool) error {
	if ctx == nil || tx == nil || userID <= 0 {
		return ErrUnauthorized
	}
	if err := s.users.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		return err
	}
	var admin, banned int
	var until sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,banned_until FROM users WHERE id=?`, userID).Scan(&admin, &banned, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if admin != 0 || banned != 0 && (!until.Valid || until.Int64 > nowMS/1000) {
		return ErrForbidden
	}
	if newWork {
		if err := s.gate.AuthorizeUserActivity(ctx, tx, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) authorizeAdminTx(ctx context.Context, tx *sql.Tx, actorID int64) error {
	if ctx == nil || tx == nil || actorID <= 0 {
		return ErrUnauthorized
	}
	if err := s.admins.AuthorizeAdmin(ctx, tx, actorID); err != nil {
		return err
	}
	var admin int
	err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, actorID).Scan(&admin)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if admin != 1 {
		return ErrForbidden
	}
	return nil
}

func (s *Service) admissionTx(ctx context.Context, tx *sql.Tx, userID, nowMS int64) error {
	if err := s.authorizeUserTx(ctx, tx, userID, nowMS, true); err != nil {
		return err
	}
	var start, end sql.NullInt64
	var paused int
	if err := tx.QueryRowContext(ctx, `SELECT starts_at,ends_at,paused FROM limited_activity_configs WHERE activity_key='fat-fish'`).Scan(&start, &end, &paused); err != nil {
		return err
	}
	if paused != 0 || !start.Valid || !end.Valid || nowMS < start.Int64*1000 || nowMS >= end.Int64*1000 {
		return ErrClosed
	}
	return nil
}

func availablePeriod(state string, start, end int64, nowMS int64) bool {
	return state == "open" && nowMS >= start*1000 && nowMS < end*1000
}

func withinBudget(deadline time.Time) bool { return time.Now().Before(deadline) }
