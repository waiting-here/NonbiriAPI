package duel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type enqueueBody struct {
	Mode              string          `json:"mode"`
	ExpectedTermsHash string          `json:"expected_terms_hash"`
	DeviceToken       string          `json:"device_token"`
	Loadout           json.RawMessage `json:"loadout,omitempty"`
}
type cancelBody struct {
	ExpectedRevision string `json:"expected_revision"`
}

func (s *Service) Enqueue(ctx context.Context, in EnqueueInput) (MutationResult, error) {
	if s.descriptor.ResolveMode(in.Mode) != nil || len(in.ExpectedTermsHash) != 64 {
		return MutationResult{}, ErrInvalidRequest
	}
	loadout, err := s.rules.Loadout(in.Mode, in.Loadout)
	if err != nil {
		return MutationResult{}, err
	}
	if s.rules.ID() == "bidding" {
		loadout = nil
	}
	device, err := s.device(in.DeviceToken)
	if err != nil {
		return MutationResult{}, err
	}
	body := enqueueBody{Mode: in.Mode, ExpectedTermsHash: in.ExpectedTermsHash, DeviceToken: in.DeviceToken, Loadout: loadout}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, in.Identity); err != nil {
		return MutationResult{}, err
	}
	route := "/api/games/" + s.rules.ID() + "/queue"
	if replay, err := s.probeReplay(ctx, tx, in.Identity, in.IdempotencyKey, "POST", route, "", body, now); err != nil {
		return MutationResult{}, err
	} else if replay != nil {
		return *replay, nil
	}
	maintenance, err := maintenanceOn(ctx, tx)
	if err != nil {
		return MutationResult{}, err
	}
	if maintenance {
		return MutationResult{}, ErrMaintenance
	}
	cfg, err := s.config(ctx, tx)
	if err != nil {
		return MutationResult{}, err
	}
	m, ok := cfg.Modes[in.Mode]
	if !ok || !cfg.Enabled || !m.Enabled {
		return MutationResult{}, ErrConflict
	}
	terms, hash, ticket, err := s.terms(in.Mode, m)
	if err != nil {
		return MutationResult{}, err
	}
	if hash != in.ExpectedTermsHash {
		return MutationResult{}, ErrConflict
	}
	allowed, err := eligible(ctx, tx, in.UserID, now)
	if err != nil {
		return MutationResult{}, err
	}
	if !allowed {
		return MutationResult{}, ErrForbidden
	}
	var occupied int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_duel_user_slots WHERE user_id=? AND game_key=?`, in.UserID, s.rules.ID()).Scan(&occupied); err != nil {
		return MutationResult{}, err
	}
	if occupied != 0 {
		return MutationResult{}, ErrConflict
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_duel_queue WHERE game_key=?`, s.rules.ID()).Scan(&count); err != nil {
		return MutationResult{}, err
	}
	if count >= QueueCapacity {
		return MutationResult{}, ErrResourceLimit
	}
	start, _, err := s.limiter.Reserve(in.UserID)
	if err != nil {
		if errors.Is(err, game.ErrStartRateLimited) {
			return MutationResult{}, ErrRateLimited
		}
		return MutationResult{}, ErrUnavailable
	}
	defer start.Release()
	done, err := s.reserveAction(in.UserID)
	if err != nil {
		return MutationResult{}, err
	}
	committed := false
	defer func() { done(committed) }()
	general, err := ledger.UserAssetAccount(ctx, tx, in.UserID, ledger.General)
	if err != nil {
		return MutationResult{}, classify(err)
	}
	gameWallet, err := ledger.UserAssetAccount(ctx, tx, in.UserID, ledger.Game)
	if err != nil {
		return MutationResult{}, classify(err)
	}
	gamePaid := gameWallet.Balance.Big()
	if gamePaid.Sign() < 0 {
		gamePaid.SetInt64(0)
	}
	if gamePaid.Cmp(big.NewInt(ticket)) > 0 {
		gamePaid.SetInt64(ticket)
	}
	needGeneral := new(big.Int).Sub(big.NewInt(ticket), gamePaid)
	availableGeneral := general.Balance.Big()
	if availableGeneral.Sign() < 0 {
		availableGeneral.SetInt64(0)
	}
	if needGeneral.Cmp(availableGeneral) > 0 {
		return MutationResult{}, ErrInsufficientCredits
	}
	if err := s.ensureCatalog(ctx, tx, in.Mode); err != nil {
		return MutationResult{}, err
	}
	id, err := s.generate(s.queuePrefix)
	if err != nil {
		return MutationResult{}, err
	}
	meta, err := s.meta(in.UserID, now)
	if err != nil {
		return MutationResult{}, err
	}
	q := queueRecord{ID: id, Mode: in.Mode, User: in.UserID, Revision: one(), Created: now, Deadline: now + QueueSeconds, Terms: terms, TermsHash: hash, Ticket: ticket, GamePaid: gamePaid.Int64(), Operation: meta.OperationID, Device: device, IP: keyed(s.ipKey, in.CanonicalSourceIP[:]), Loadout: loadout}
	err = s.finance.QueueReserve(ctx, tx, finance.Entry{Meta: meta, ResourceID: id, UserID: q.User, Amount: ledger.AmountFromMilli(ticket), GamePaid: ledger.AmountFromMilli(q.GamePaid)}, func(ctx context.Context, tx *sql.Tx, accounts ledger.AccountPair) error {
		q.GeneralAccount = accounts.General
		q.GameAccount = accounts.Game
		return s.insertQueue(ctx, tx, q)
	})
	if err != nil {
		return MutationResult{}, classify(err)
	}
	d, err := s.replay(ctx, tx, in.Identity, in.IdempotencyKey, "POST", route, "", body, now)
	if err != nil {
		return MutationResult{}, err
	}
	result, err := finish(ctx, tx, d, 202, QueueReceipt{QueueID: id, Revision: q.Revision.Decimal(), Deadline: q.Deadline})
	if err != nil {
		return MutationResult{}, err
	}
	committed = true
	start.Commit()
	s.publish(ctx, activities.PublishFacts{AccountIDs: []int64{q.User}})
	return result, nil
}
func (s *Service) releaseQueue(ctx context.Context, tx *sql.Tx, q queueRecord, actor, now int64) error {
	meta, err := s.meta(actor, now)
	if err != nil {
		return err
	}
	return classify(s.finance.QueueRelease(ctx, tx, finance.Entry{Meta: meta, ResourceID: q.ID, UserID: q.User, Amount: ledger.AmountFromMilli(q.Ticket), GamePaid: ledger.AmountFromMilli(q.GamePaid)}, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE game_duel_queue SET ledger_rows_remaining=? WHERE id=? AND revision=?`, db.EncodeU128(db.U128{}), q.ID, db.EncodeU128(q.Revision))
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM game_duel_user_slots WHERE queue_id=? AND user_id=? AND game_key=?`, q.ID, q.User, s.rules.ID()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM game_duel_queue WHERE id=?`, q.ID)
		return err
	}))
}
func (s *Service) CancelQueue(ctx context.Context, in CancelInput) (MutationResult, error) {
	expected, err := db.ParseU128Decimal(in.ExpectedRevision)
	if err != nil || expected.Big().Sign() <= 0 || !db.ValidateOpaqueID(in.QueueID, s.queuePrefix) {
		return MutationResult{}, ErrInvalidRequest
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, in.Identity); err != nil {
		return MutationResult{}, err
	}
	body := cancelBody{ExpectedRevision: in.ExpectedRevision}
	route := "/api/games/" + s.rules.ID() + "/queue/{id}"
	if replay, err := s.probeReplay(ctx, tx, in.Identity, in.IdempotencyKey, "DELETE", route, in.QueueID, body, now); err != nil {
		return MutationResult{}, err
	} else if replay != nil {
		return *replay, nil
	}
	q, err := s.queue(ctx, tx, in.QueueID)
	if errors.Is(err, sql.ErrNoRows) {
		return MutationResult{}, ErrConflict
	}
	if err != nil {
		return MutationResult{}, classify(err)
	}
	if q.User != in.UserID {
		return MutationResult{}, ErrNotFound
	}
	if q.Revision != expected {
		return MutationResult{}, ErrConflict
	}
	done, err := s.reserveAction(in.UserID)
	if err != nil {
		return MutationResult{}, err
	}
	committed := false
	defer func() { done(committed) }()
	if err := s.releaseQueue(ctx, tx, q, in.UserID, now); err != nil {
		return MutationResult{}, err
	}
	d, err := s.replay(ctx, tx, in.Identity, in.IdempotencyKey, "DELETE", route, in.QueueID, body, now)
	if err != nil {
		return MutationResult{}, err
	}
	result, err := finish(ctx, tx, d, 204, nil)
	if err != nil {
		return MutationResult{}, err
	}
	committed = true
	s.publish(ctx, activities.PublishFacts{AccountIDs: []int64{q.User}})
	return result, nil
}
