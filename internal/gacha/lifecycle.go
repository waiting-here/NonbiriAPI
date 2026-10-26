package gacha

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func (s *Service) History(ctx context.Context, user int64, cursor string) (HistoryPage, error) {
	out := HistoryPage{Items: []DrawReceipt{}}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	before, id := at+1, ""
	if cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil {
			return out, ErrInvalid
		}
		parts := strings.Split(string(raw), ":")
		if len(parts) != 2 || !db.ValidateOpaqueID(parts[1], "op_") {
			return out, ErrInvalid
		}
		before, e = strconv.ParseInt(parts[0], 10, 64)
		if e != nil || before < 0 || before > 253402300799 {
			return out, ErrInvalid
		}
		id = parts[1]
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, user, at); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT receipt FROM gacha_draws WHERE user_id=? AND created_at>? AND (created_at<? OR created_at=? AND operation_id<?) ORDER BY created_at DESC,operation_id DESC LIMIT 51`,
		user, at-historyDays*86400, before, before, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var raw string
		var receipt DrawReceipt
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &receipt); err != nil {
			break
		}
		out.Items = append(out.Items, receipt)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Items) > 50 {
		out.Items = out.Items[:50]
		last := out.Items[49]
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(last.CreatedAt, 10) + ":" + last.ID))
	}
	return out, tx.Commit()
}

type OperationExport struct {
	ID                string   `json:"id"`
	Action            string   `json:"action"`
	Delta             [6]int64 `json:"delta"`
	Balance           [6]int64 `json:"balance_after"`
	CreditOperationID *string  `json:"credit_operation_id"`
	CreatedAt         int64    `json:"created_at"`
}
type DrawExport struct {
	Receipt        DrawReceipt `json:"receipt"`
	Rules          Rules       `json:"rules"`
	Roster         []string    `json:"roster"`
	CatalogVersion int         `json:"catalog_version"`
}
type UserExport struct {
	EnteredAt         int64             `json:"entered_at"`
	CheckedDay        string            `json:"checked_day"`
	LimitedCheckedDay string            `json:"limited_checked_day"`
	WelcomeGranted    bool              `json:"welcome_granted"`
	Month             string            `json:"month"`
	Balance           [6]int64          `json:"balance"`
	BalanceAssets     [6]string         `json:"balance_assets"`
	Newcomer          bool              `json:"newcomer"`
	MonthlyUsed       [2]int64          `json:"monthly_used"`
	Owned             map[string]Owned  `json:"owned"`
	Pity              map[string]Pity   `json:"pity"`
	Claims            []ClaimExport     `json:"claims"`
	Operations        []OperationExport `json:"operations"`
	Draws             []DrawExport      `json:"draws"`
}
type ClaimExport struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	At     int64  `json:"created_at"`
}

func (s *Service) ExportUserTx(ctx context.Context, tx *sql.Tx, user, at int64, limit int) (UserExport, error) {
	out := UserExport{BalanceAssets: [6]string{"permanent", "limited", "N", "R", "SR", "SSR"}, Claims: []ClaimExport{}, Operations: []OperationExport{}, Draws: []DrawExport{}}
	if user <= 0 || limit < 1 || limit > lifecycle.CollectionLimit {
		return out, ErrInvalid
	}
	p, _, err := readProfile(ctx, tx, user)
	if err != nil {
		return out, err
	}
	out.EnteredAt = p.EnteredAt
	out.CheckedDay = p.Day
	out.LimitedCheckedDay = p.LimitedDay
	out.WelcomeGranted = p.WelcomeGranted
	out.Month = p.Month
	out.Balance = p.Balance
	out.Newcomer = p.Newcomer
	out.MonthlyUsed = p.Used
	out.Owned, err = readOwned(ctx, tx, user)
	if err != nil {
		return out, err
	}
	out.Pity, err = readPity(ctx, tx, user)
	if err != nil {
		return out, err
	}
	if len(out.Owned) > limit || len(out.Pity) > limit {
		return out, ErrExportLimit
	}
	rows, err := tx.QueryContext(ctx, "SELECT kind,target,created_at FROM gacha_claims WHERE user_id=? ORDER BY kind,target LIMIT ?", user, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v ClaimExport
		if err = rows.Scan(&v.Kind, &v.Target, &v.At); err != nil {
			break
		}
		out.Claims = append(out.Claims, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Claims) > limit {
		return out, ErrExportLimit
	}
	rows, err = tx.QueryContext(ctx, "SELECT id,action,delta,balance_after,credit_operation_id,created_at FROM gacha_operations WHERE user_id=? ORDER BY created_at,id LIMIT ?", user, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v OperationExport
		var delta, balance string
		if err = rows.Scan(&v.ID, &v.Action, &delta, &balance, &v.CreditOperationID, &v.CreatedAt); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(delta), &v.Delta); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(balance), &v.Balance); err != nil {
			break
		}
		out.Operations = append(out.Operations, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Operations) > limit {
		return out, ErrExportLimit
	}
	rows, err = tx.QueryContext(ctx, "SELECT receipt,rules,roster,catalog_version FROM gacha_draws WHERE user_id=? AND created_at>? ORDER BY created_at,operation_id LIMIT ?", user, at-historyDays*86400, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v DrawExport
		var receipt, rules, roster string
		if err = rows.Scan(&receipt, &rules, &roster, &v.CatalogVersion); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(receipt), &v.Receipt); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(rules), &v.Rules); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(roster), &v.Roster); err != nil {
			break
		}
		out.Draws = append(out.Draws, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Draws) > limit {
		return out, ErrExportLimit
	}
	return out, nil
}
func (s *Service) DeleteUserTx(ctx context.Context, tx *sql.Tx, user int64) error {
	for _, table := range []string{"gacha_card_participants", "gacha_pool_participants", "gacha_draws", "gacha_claims", "gacha_pity", "gacha_owned", "gacha_profiles"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id=?", user); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE gacha_operations SET user_id=NULL,key_hash=NULL,request_hash=NULL WHERE user_id=?", user); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE gacha_config_events SET actor_user_id=NULL WHERE actor_user_id=?", user)
	return err
}
func (s *Service) Retain(ctx context.Context, at int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	var out lifecycle.WorkResult
	if limit < 1 {
		return out, ErrInvalid
	}
	if time.Now().After(deadline) {
		return out, context.DeadlineExceeded
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result, err := s.config.Database.ExecContext(ctx, `DELETE FROM gacha_draws WHERE operation_id IN (SELECT operation_id FROM gacha_draws WHERE created_at<=? ORDER BY created_at LIMIT ?)`, at-historyDays*86400, limit)
	if err != nil {
		return out, err
	}
	count, err := result.RowsAffected()
	out.Processed = int(count)
	out.More = out.Processed == limit
	return out, err
}
