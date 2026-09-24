package limitedactivities

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (s *Service) beginReplay(ctx context.Context, tx *sql.Tx, actorKind string, actor int64, key, method, route, activity string, body any, now int64) (idempotency.Decision, error) {
	a, err := idempotency.ActorScopeHash(actorKind, strconv.FormatInt(actor, 10))
	if err != nil {
		return idempotency.Decision{}, err
	}
	raw, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: a, Method: method, Route: route, PathResourceIDs: []string{activity}, Body: raw})
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	secret, err := s.keys.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/limited-activity-idempotency/v1"))
	if err != nil {
		return idempotency.Decision{}, err
	}
	defer clear(secret)
	if len(secret) != 32 {
		return idempotency.Decision{}, ErrInvariant
	}
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write(digest[:])
	copy(digest[:], h.Sum(nil))
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: a, Key: key, RequestHash: digest, DecisionNow: now})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return decision, ErrConflict
	}
	return decision, err
}

func completeReplay(ctx context.Context, tx *sql.Tx, d idempotency.Decision, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return idempotency.Complete(ctx, tx, d, http.StatusOK, raw)
}

func (s *Service) Exchange(ctx context.Context, user int64, key string, input ExchangeInput) (MutationResult[ExchangeResult], error) {
	var result MutationResult[ExchangeResult]
	if _, err := idempotency.KeyHash(key); err != nil {
		return result, ErrInvalid
	}
	if !input.Asset.IsActivity() {
		return result, ErrInvalid
	}
	quantity, err := db.ParseU128Decimal(input.Quantity)
	if err != nil || input.Quantity != quantity.Decimal() || quantity.Big().Sign() <= 0 {
		return result, ErrInvalid
	}
	assetMilli, err := ledger.AmountFromBig(new(big.Int).Mul(quantity.Big(), big.NewInt(1000)))
	if err != nil {
		return result, ErrInvalid
	}
	now, err := s.decisionNow()
	if err != nil {
		return result, err
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return result, err
	}
	if err = s.gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return result, err
	}
	decision, err := s.beginReplay(ctx, tx, "user", user, key, http.MethodPost, userExchangeRoute, PictureBook, input, now)
	if err != nil {
		return result, err
	}
	if decision.Kind == idempotency.Replay {
		if err = json.Unmarshal(decision.ResponseBody, &result.Value); err != nil {
			return result, ErrInvariant
		}
		result.Replayed = true
		return result, tx.Commit()
	}
	if _, err = s.CheckAdmissionTx(ctx, tx, user, PictureBook, now); err != nil {
		return result, err
	}
	c, err := readConfig(ctx, tx, PictureBook)
	if err != nil {
		return result, err
	}
	settings, paper, brush, err := decodeSettings(c.module)
	if err != nil {
		return result, ErrInvariant
	}
	unit := paper
	if input.Asset == ledger.SketchBrush {
		unit = brush
	}
	cost, err := ledger.AmountFromBig(new(big.Int).Mul(quantity.Big(), big.NewInt(unit)))
	if err != nil {
		return result, ErrInvalid
	}
	var totalRaw, capRaw []byte
	var stateRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT total_exchanged_mag,cap_mag,revision FROM activity_exchange_state WHERE activity_key=? AND asset_type=?`, PictureBook, string(input.Asset)).Scan(&totalRaw, &capRaw, &stateRevision); err != nil {
		return result, err
	}
	total, err := db.DecodeU128(totalRaw)
	if err != nil || stateRevision == math.MaxInt64 {
		return result, ErrInvariant
	}
	nextBig := new(big.Int).Add(total.Big(), quantity.Big())
	next, err := db.U128FromBig(nextBig)
	if err != nil {
		return result, ErrCapacity
	}
	if capRaw != nil {
		cap, e := db.DecodeU128(capRaw)
		if e != nil {
			return result, ErrInvariant
		}
		if nextBig.Cmp(cap.Big()) > 0 {
			return result, ErrCapacity
		}
	}
	wallets, err := ledger.CreateSketchAccounts(ctx, tx, user, now)
	if err != nil {
		return result, err
	}
	general, err := ledger.UserAccount(ctx, tx, user)
	if err != nil {
		return result, err
	}
	external, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.General)
	if err != nil {
		return result, err
	}
	assetExternal, err := ledger.CodedAssetAccount(ctx, tx, "external", input.Asset)
	if err != nil {
		return result, err
	}
	account := wallets.Paper
	if input.Asset == ledger.SketchBrush {
		account = wallets.Brush
	}
	op, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return result, err
	}
	plan, err := ledger.NewActivityExchange(ledger.Meta{OperationID: op, ActorUserID: user, CreatedAt: now}, general.ID, external.ID, account, assetExternal.ID, input.Asset, cost, assetMilli)
	if err != nil {
		return result, err
	}
	posted, err := ledger.Apply(ctx, tx, plan)
	if err != nil {
		return result, err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE activity_exchange_state SET total_exchanged_mag=?,revision=revision+1 WHERE activity_key=? AND asset_type=? AND revision=?`, db.EncodeU128(next), PictureBook, string(input.Asset), stateRevision)
	if err != nil {
		return result, err
	}
	if err = oneRow(changed); err != nil {
		return result, err
	}
	costMag, err := db.U128FromBig(cost.Big())
	if err != nil {
		return result, ErrInvariant
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO activity_exchange_receipts(operation_id,activity_key,config_revision,user_id,asset_type,quantity_mag,unit_price_milli,cost_mag,ledger_seq,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, op, PictureBook, c.revision, user, string(input.Asset), db.EncodeU128(quantity), unit, db.EncodeU128(costMag), posted.LedgerSeq, now)
	if err != nil {
		return result, err
	}
	result.Value.Receipt = Receipt{op, PictureBook, strconv.FormatInt(c.revision, 10), input.Asset, input.Quantity, points(big.NewInt(unit)), points(cost.Big()), strconv.FormatInt(posted.LedgerSeq, 10), now}
	result.Value.Wallet, err = walletTx(ctx, tx, user)
	if err != nil {
		return result, err
	}
	result.Value.Supply, err = readSupply(ctx, tx, PictureBook, settings)
	if err != nil {
		return result, err
	}
	if !nilInterface(s.activity) {
		if err = s.activity.RecordLimitedActivityTx(ctx, tx, user, now); err != nil {
			return result, err
		}
	}
	if err = completeReplay(ctx, tx, decision, result.Value); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *Service) AdminConfig(ctx context.Context, admin int64, key string) (Detail, error) {
	now, err := s.decisionNow()
	if err != nil {
		return Detail{}, err
	}
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Detail{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, admin); err != nil {
		return Detail{}, err
	}
	d, err := s.detailTx(ctx, tx, key, now)
	if err != nil {
		return Detail{}, err
	}
	return d, tx.Commit()
}

func (s *Service) authorizeAdminTx(ctx context.Context, tx *sql.Tx, admin int64) error {
	if admin <= 0 {
		return authz.ErrUnauthorized
	}
	if err := s.admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return err
	}
	var isAdmin int
	if err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, admin).Scan(&isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authz.ErrUnauthorized
		}
		return err
	}
	if isAdmin != 1 {
		return authz.ErrForbidden
	}
	return nil
}

func (s *Service) UpdateConfig(ctx context.Context, admin int64, activity, key string, input ConfigInput) (MutationResult[Detail], error) {
	var result MutationResult[Detail]
	if _, err := idempotency.KeyHash(key); err != nil {
		return result, ErrInvalid
	}
	d, err := s.registry.lookup(activity)
	if err != nil {
		return result, err
	}
	revision, err := strconv.ParseInt(input.ExpectedRevision, 10, 64)
	if err != nil || revision < 1 || revision == math.MaxInt64 || strconv.FormatInt(revision, 10) != input.ExpectedRevision {
		return result, ErrInvalid
	}
	if (input.StartsAt == nil) != (input.EndsAt == nil) || input.StartsAt != nil && (*input.StartsAt < 0 || *input.EndsAt > maxUnix || *input.StartsAt >= *input.EndsAt) {
		return result, ErrInvalid
	}
	input.ModuleConfig, err = d.configuration.Normalize(input.ModuleConfig)
	if err != nil {
		return result, err
	}
	raw := input.ModuleConfig
	now, err := s.decisionNow()
	if err != nil {
		return result, err
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, admin); err != nil {
		return result, err
	}
	decision, err := s.beginReplay(ctx, tx, "admin", admin, key, http.MethodPut, adminConfigRoute, activity, input, now)
	if err != nil {
		return result, err
	}
	if decision.Kind == idempotency.Replay {
		if err = json.Unmarshal(decision.ResponseBody, &result.Value); err != nil {
			return result, ErrInvariant
		}
		result.Replayed = true
		return result, tx.Commit()
	}
	old, err := readConfig(ctx, tx, activity)
	if err != nil {
		return result, err
	}
	if old.revision != revision {
		return result, ErrConflict
	}
	var finalizer Finalizer
	if input.Paused && !old.paused && !nilInterface(d.runtime) {
		finalizer, err = d.runtime.PreparePauseTx(ctx, tx, now)
		if !nilInterface(finalizer) {
			defer finalizer.Abort()
		}
		if err != nil {
			return result, err
		}
	}
	changed, err := tx.ExecContext(ctx, `UPDATE limited_activity_configs SET visible=?,starts_at=?,ends_at=?,paused=?,module_config=?,revision=revision+1,updated_at=? WHERE activity_key=? AND revision=?`, input.Visible, input.StartsAt, input.EndsAt, input.Paused, string(raw), now, activity, revision)
	if err != nil {
		return result, err
	}
	if err = oneRow(changed); err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO limited_activity_revisions(activity_key,revision,visible,starts_at,ends_at,paused,module_config,actor_user_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, activity, revision+1, input.Visible, input.StartsAt, input.EndsAt, input.Paused, string(raw), admin, now)
	if err != nil {
		return result, err
	}
	if err = d.configuration.ApplyTx(ctx, tx, activity, raw); err != nil {
		return result, err
	}
	result.Value, err = s.detailTx(ctx, tx, activity, now)
	if err != nil {
		return result, err
	}
	if err = completeReplay(ctx, tx, decision, result.Value); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if !nilInterface(finalizer) {
		finalizer.Commit()
	}
	return result, nil
}

func oneRow(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
