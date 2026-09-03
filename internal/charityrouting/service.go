package charityrouting

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	maxUnixSecond    = int64(253402300799)
	maxNameRunes     = 64
	maxBindingBatch  = 256
	maxBindingOrd    = 255
	maxPageLimit     = 100
	defaultPageLimit = 50
)

const (
	routeCapability          = "/api/charity/models"
	routeAdminModels         = "/admin/api/charity-models"
	routeAdminModel          = "/admin/api/charity-models/{id}"
	routeAdminCandidates     = "/admin/api/charity-models/{id}/binding-candidates"
	routeAdminBindings       = "/admin/api/charity-models/{id}/bindings"
	routeAdminBindingBatch   = "/admin/api/charity-models/{id}/bindings/batch"
	routeAdminBindingOrder   = "/admin/api/charity-models/{id}/bindings/order"
	routeAdminBinding        = "/admin/api/charity-models/{id}/bindings/{bindingId}"
	routeStewardModels       = "/api/steward/charity-models"
	routeStewardModel        = "/api/steward/charity-models/{id}"
	routeStewardCandidates   = "/api/steward/charity-models/{id}/binding-candidates"
	routeStewardBindings     = "/api/steward/charity-models/{id}/bindings"
	routeStewardBindingBatch = "/api/steward/charity-models/{id}/bindings/batch"
	routeStewardBindingOrder = "/api/steward/charity-models/{id}/bindings/order"
	routeStewardBinding      = "/api/steward/charity-models/{id}/bindings/{bindingId}"
)

type Config struct {
	Store         *db.Store
	RoleAuth      RoleFinalTxAuthorizer
	DonationState DonationStateOwner
	CursorKeys    resources.CursorKeyDeriver
	Entropy       io.Reader
	Now           func() time.Time
}

type Service struct {
	db            *sql.DB
	roleAuth      RoleFinalTxAuthorizer
	donationState DonationStateOwner
	cursorKeys    resources.CursorKeyDeriver
	entropy       io.Reader
	now           func() time.Time
}

func New(config Config) (*Service, error) {
	if config.Store == nil || config.Store.DB() == nil || nilDependency(config.DonationState) {
		return nil, errors.New("charity routing: store and donation state owner are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if nilDependency(config.Entropy) {
		config.Entropy = cryptorand.Reader
	}
	return &Service{db: config.Store.DB(), roleAuth: config.RoleAuth, donationState: config.DonationState,
		cursorKeys: config.CursorKeys, entropy: config.Entropy, now: config.Now}, nil
}

func (s *Service) CreateAdmin(ctx context.Context, mutation resources.ControlMutation, input ModelCreate) (resources.MutationResult[AdminCharityModel], error) {
	if !validMutation(mutation, http.MethodPost, routeAdminModels) {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
	}
	return s.create(ctx, roleAdmin, 0, mutation, input)
}

func (s *Service) CreateSteward(ctx context.Context, actorUserID int64, mutation resources.ControlMutation, input ModelCreate) (resources.MutationResult[StewardCharityModel], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodPost, routeStewardModels) {
		return resources.MutationResult[StewardCharityModel]{}, ErrInvalidRequest
	}
	admin, err := s.create(ctx, roleSteward, actorUserID, mutation, input)
	if err != nil {
		return resources.MutationResult[StewardCharityModel]{}, err
	}
	value := stewardModel(admin.Value)
	body, err := json.Marshal(value)
	if err != nil {
		return resources.MutationResult[StewardCharityModel]{}, ErrUnavailable
	}
	return resources.MutationResult[StewardCharityModel]{Value: value, Status: admin.Status, Body: body, Replayed: admin.Replayed}, nil
}

func (s *Service) create(ctx context.Context, role roleKind, actorUserID int64, mutation resources.ControlMutation, input ModelCreate) (resources.MutationResult[AdminCharityModel], error) {
	prices, err := validateModelCreate(input)
	if s == nil || ctx == nil || err != nil {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, actorUserID)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginMutation(ctx, tx, role, actorID, mutation, now)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[AdminCharityModel](decision)
	}
	fullName := "[公益]" + input.Provider + "/" + input.Model
	result, err := tx.ExecContext(ctx, `INSERT INTO charity_models(
provider,model,full_name,enabled,pricing_mode,request_user_price,request_donor_reward,
uncached_user_price,cache_write_user_price,cache_read_user_price,output_user_price,
uncached_donor_reward,cache_write_donor_reward,cache_read_donor_reward,output_donor_reward,
discount_percent,discount_start_at,discount_end_at,discount_enabled,flatten_tool_calls,
created_by_user_id,revision,binding_revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,0,?,?)`,
		input.Provider, input.Model, fullName, boolInt(input.Enabled), input.Pricing.Mode,
		prices.requestUser, prices.requestReward, prices.user[0], prices.user[1], prices.user[2], prices.user[3],
		prices.reward[0], prices.reward[1], prices.reward[2], prices.reward[3], input.Discount.Percent,
		input.Discount.StartAt, input.Discount.EndAt, boolInt(input.Discount.Enabled), boolInt(input.FlattenToolCalls),
		actorID, now, now)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, classifyWrite("create charity model", err)
	}
	modelID, err := result.LastInsertId()
	if err != nil || modelID <= 0 {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvariant
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO charity_model_stats(model_id) VALUES(?)`, modelID); err != nil {
		return resources.MutationResult[AdminCharityModel]{}, fmt.Errorf("charity routing: initialize stats: %w", err)
	}
	if input.FlattenToolCalls {
		if err := insertPolicyAudit(ctx, tx, actorID, string(role), modelID, false, true, now); err != nil {
			return resources.MutationResult[AdminCharityModel]{}, err
		}
	}
	value, err := getAdminModelTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusCreated, value)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	return out, nil
}

func (s *Service) PatchAdmin(ctx context.Context, modelID int64, mutation resources.ControlMutation, input ModelPatch) (resources.MutationResult[AdminCharityModel], error) {
	if !validMutation(mutation, http.MethodPatch, routeAdminModel, modelID) {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
	}
	return s.patch(ctx, roleAdmin, 0, modelID, mutation, input)
}

func (s *Service) PatchSteward(ctx context.Context, actorUserID, modelID int64, mutation resources.ControlMutation, input ModelPatch) (resources.MutationResult[StewardCharityModel], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodPatch, routeStewardModel, modelID) {
		return resources.MutationResult[StewardCharityModel]{}, ErrInvalidRequest
	}
	admin, err := s.patch(ctx, roleSteward, actorUserID, modelID, mutation, input)
	if err != nil {
		return resources.MutationResult[StewardCharityModel]{}, err
	}
	value := stewardModel(admin.Value)
	body, err := json.Marshal(value)
	if err != nil {
		return resources.MutationResult[StewardCharityModel]{}, ErrUnavailable
	}
	return resources.MutationResult[StewardCharityModel]{Value: value, Status: admin.Status, Body: body, Replayed: admin.Replayed}, nil
}

func (s *Service) patch(ctx context.Context, role roleKind, actorUserID, modelID int64, mutation resources.ControlMutation, input ModelPatch) (resources.MutationResult[AdminCharityModel], error) {
	expected, err := parsePositiveID(input.ExpectedRevision)
	if s == nil || ctx == nil || modelID <= 0 || err != nil || !validateModelPatch(input) {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, actorUserID)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginMutation(ctx, tx, role, actorID, mutation, now)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[AdminCharityModel](decision)
	}
	current, err := readStoredModelTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	if current.revision != expected {
		return resources.MutationResult[AdminCharityModel]{}, ErrConflict
	}
	updated := current
	if input.Provider != nil {
		updated.provider = *input.Provider
	}
	if input.Model != nil {
		updated.model = *input.Model
	}
	if input.Enabled != nil {
		updated.enabled = boolInt(*input.Enabled)
	}
	if input.Pricing != nil {
		prices, err := validatePricing(*input.Pricing)
		if err != nil {
			return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
		}
		updated.mode = input.Pricing.Mode
		updated.requestUser, updated.requestReward = prices.requestUser, prices.requestReward
		updated.user, updated.reward = prices.user, prices.reward
	}
	if input.Discount != nil {
		if !validDiscountPatch(*input.Discount) {
			return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
		}
		if input.Discount.Enabled != nil {
			updated.discountEnabled = boolInt(*input.Discount.Enabled)
		}
		if input.Discount.Percent != nil {
			updated.discountPercent = *input.Discount.Percent
		}
		if input.Discount.StartAt != nil {
			updated.discountStart = nullableInt(*input.Discount.StartAt)
		}
		if input.Discount.EndAt != nil {
			updated.discountEnd = nullableInt(*input.Discount.EndAt)
		}
		if !validDiscount(DiscountInput{Enabled: updated.discountEnabled == 1, Percent: updated.discountPercent,
			StartAt: nullIntPointer(updated.discountStart), EndAt: nullIntPointer(updated.discountEnd)}) {
			return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
		}
	}
	if input.FlattenToolCalls != nil {
		updated.flatten = boolInt(*input.FlattenToolCalls)
	}
	if !validModelName(updated.provider) || !validModelName(updated.model) {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
	}
	fullName := "[公益]" + updated.provider + "/" + updated.model
	result, err := tx.ExecContext(ctx, `UPDATE charity_models SET
provider=?,model=?,full_name=?,enabled=?,pricing_mode=?,request_user_price=?,request_donor_reward=?,
uncached_user_price=?,cache_write_user_price=?,cache_read_user_price=?,output_user_price=?,
uncached_donor_reward=?,cache_write_donor_reward=?,cache_read_donor_reward=?,output_donor_reward=?,
discount_percent=?,discount_start_at=?,discount_end_at=?,discount_enabled=?,flatten_tool_calls=?,
revision=revision+1,updated_at=? WHERE id=? AND revision=?`,
		updated.provider, updated.model, fullName, updated.enabled, updated.mode, updated.requestUser, updated.requestReward,
		updated.user[0], updated.user[1], updated.user[2], updated.user[3], updated.reward[0], updated.reward[1],
		updated.reward[2], updated.reward[3], updated.discountPercent, nullableSQL(updated.discountStart),
		nullableSQL(updated.discountEnd), updated.discountEnabled, updated.flatten, now, modelID, expected)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, classifyWrite("patch charity model", err)
	}
	if err := requireOne(result); err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	if current.flatten != updated.flatten {
		if err := insertPolicyAudit(ctx, tx, actorID, string(role), modelID, current.flatten == 1, updated.flatten == 1, now); err != nil {
			return resources.MutationResult[AdminCharityModel]{}, err
		}
	}
	value, err := getAdminModelTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	return out, nil
}

func (s *Service) DeleteAdmin(ctx context.Context, modelID int64, mutation resources.ControlMutation, input ModelDelete) (resources.MutationResult[struct{}], error) {
	if !validMutation(mutation, http.MethodDelete, routeAdminModel, modelID) {
		return resources.MutationResult[struct{}]{}, ErrInvalidRequest
	}
	return s.deleteModel(ctx, roleAdmin, 0, modelID, mutation, input)
}

func (s *Service) DeleteSteward(ctx context.Context, actorUserID, modelID int64, mutation resources.ControlMutation, input ModelDelete) (resources.MutationResult[struct{}], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodDelete, routeStewardModel, modelID) {
		return resources.MutationResult[struct{}]{}, ErrInvalidRequest
	}
	return s.deleteModel(ctx, roleSteward, actorUserID, modelID, mutation, input)
}

func (s *Service) deleteModel(ctx context.Context, role roleKind, actorUserID, modelID int64, mutation resources.ControlMutation, input ModelDelete) (resources.MutationResult[struct{}], error) {
	expected, err := parsePositiveID(input.ExpectedRevision)
	if s == nil || ctx == nil || modelID <= 0 || err != nil || strings.TrimSpace(input.Confirmation) == "" {
		return resources.MutationResult[struct{}]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[struct{}]{}, err
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, actorUserID)
	if err != nil {
		return resources.MutationResult[struct{}]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginMutation(ctx, tx, role, actorID, mutation, now)
	if err != nil {
		return resources.MutationResult[struct{}]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[struct{}](decision)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM charity_models WHERE id=? AND revision=?`, modelID, expected)
	if err != nil {
		return resources.MutationResult[struct{}]{}, fmt.Errorf("charity routing: delete model: %w", err)
	}
	if err := requireOne(result); err != nil {
		return resources.MutationResult[struct{}]{}, err
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusNoContent, nil); err != nil {
		return resources.MutationResult[struct{}]{}, fmt.Errorf("charity routing: complete delete replay: %w", err)
	}
	out := resources.MutationResult[struct{}]{Status: http.StatusNoContent, Body: []byte{}}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[struct{}]{}, err
	}
	return out, nil
}

func (s *Service) GetAdmin(ctx context.Context, modelID int64) (AdminCharityModel, error) {
	if s == nil || s.db == nil || ctx == nil || modelID <= 0 {
		return AdminCharityModel{}, ErrInvalidRequest
	}
	return getAdminModelDB(ctx, s.db, modelID)
}

func (s *Service) GetSteward(ctx context.Context, actorUserID, modelID int64) (StewardCharityModel, error) {
	if s == nil || s.db == nil || ctx == nil || actorUserID <= 0 || modelID <= 0 || nilDependency(s.roleAuth) {
		return StewardCharityModel{}, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return StewardCharityModel{}, fmt.Errorf("charity routing: begin steward model read: %w", err)
	}
	defer tx.Rollback()
	if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorUserID); err != nil {
		return StewardCharityModel{}, mapAuthorization(err)
	}
	value, err := getAdminModelTx(ctx, tx, modelID)
	if err != nil {
		return StewardCharityModel{}, err
	}
	if err := tx.Commit(); err != nil {
		return StewardCharityModel{}, fmt.Errorf("charity routing: commit steward model read: %w", err)
	}
	return stewardModel(value), nil
}

func (s *Service) ListAdmin(ctx context.Context, query string, enabled *bool, afterID int64, limit int) ([]AdminCharityModel, int64, error) {
	return s.listModels(ctx, query, enabled, afterID, limit)
}

func (s *Service) ListSteward(ctx context.Context, actorUserID int64, query string, enabled *bool, afterID int64, limit int) ([]StewardCharityModel, int64, error) {
	items, next, err := s.listModelsForSteward(ctx, actorUserID, query, enabled, afterID, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]StewardCharityModel, len(items))
	for index := range items {
		out[index] = stewardModel(items[index])
	}
	return out, next, nil
}

func (s *Service) listModelsForSteward(ctx context.Context, actorUserID int64, query string, enabled *bool, afterID int64, limit int) ([]AdminCharityModel, int64, error) {
	if s == nil || s.db == nil || ctx == nil || actorUserID <= 0 || nilDependency(s.roleAuth) || afterID < 0 || limit < 1 || limit > maxPageLimit ||
		!utf8.ValidString(query) || utf8.RuneCountInString(query) > 128 {
		return nil, 0, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("charity routing: begin steward list: %w", err)
	}
	defer tx.Rollback()
	if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorUserID); err != nil {
		return nil, 0, mapAuthorization(err)
	}
	items, next, err := listModelsQuery(ctx, tx, query, enabled, afterID, limit)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("charity routing: commit steward list: %w", err)
	}
	return items, next, nil
}

func (s *Service) listModels(ctx context.Context, query string, enabled *bool, afterID int64, limit int) ([]AdminCharityModel, int64, error) {
	if s == nil || s.db == nil || ctx == nil || afterID < 0 || limit < 1 || limit > maxPageLimit ||
		!utf8.ValidString(query) || utf8.RuneCountInString(query) > 128 {
		return nil, 0, ErrInvalidRequest
	}
	return listModelsQuery(ctx, s.db, query, enabled, afterID, limit)
}

type modelQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func listModelsQuery(ctx context.Context, queryer modelQueryer, query string, enabled *bool, afterID int64, limit int) ([]AdminCharityModel, int64, error) {
	statement := `SELECT id FROM charity_models WHERE id>?`
	args := []any{afterID}
	if query != "" {
		statement += ` AND (provider LIKE ? ESCAPE '\' OR model LIKE ? ESCAPE '\' OR full_name LIKE ? ESCAPE '\')`
		pattern := "%" + escapeLike(query) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	if enabled != nil {
		statement += ` AND enabled=?`
		args = append(args, boolInt(*enabled))
	}
	statement += ` ORDER BY id LIMIT ?`
	args = append(args, limit+1)
	rows, err := queryer.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("charity routing: list model identities: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, 0, err
	}
	next := int64(0)
	if len(ids) > limit {
		next = ids[limit-1]
		ids = ids[:limit]
	}
	items := make([]AdminCharityModel, 0, len(ids))
	for _, id := range ids {
		item, err := scanAdminModel(queryer.QueryRowContext(ctx, modelProjectionSQL, id))
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, next, nil
}

type storedModel struct {
	id, revision, bindingRevision    int64
	provider, model, mode            string
	enabled, flatten                 int
	requestUser, requestReward       int64
	user, reward                     [4]int64
	discountPercent, discountEnabled int
	discountStart, discountEnd       sql.NullInt64
}

func readStoredModelTx(ctx context.Context, tx *sql.Tx, modelID int64) (storedModel, error) {
	var value storedModel
	err := tx.QueryRowContext(ctx, `SELECT id,provider,model,enabled,pricing_mode,
request_user_price,request_donor_reward,uncached_user_price,cache_write_user_price,cache_read_user_price,output_user_price,
uncached_donor_reward,cache_write_donor_reward,cache_read_donor_reward,output_donor_reward,
discount_percent,discount_start_at,discount_end_at,discount_enabled,flatten_tool_calls,revision,binding_revision
FROM charity_models WHERE id=?`, modelID).Scan(&value.id, &value.provider, &value.model, &value.enabled, &value.mode,
		&value.requestUser, &value.requestReward, &value.user[0], &value.user[1], &value.user[2], &value.user[3],
		&value.reward[0], &value.reward[1], &value.reward[2], &value.reward[3], &value.discountPercent,
		&value.discountStart, &value.discountEnd, &value.discountEnabled, &value.flatten, &value.revision, &value.bindingRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return storedModel{}, ErrNotFound
	}
	if err != nil {
		return storedModel{}, fmt.Errorf("charity routing: read stored model: %w", err)
	}
	return value, nil
}

func getAdminModelDB(ctx context.Context, database *sql.DB, modelID int64) (AdminCharityModel, error) {
	return scanAdminModel(database.QueryRowContext(ctx, modelProjectionSQL, modelID))
}

func getAdminModelTx(ctx context.Context, tx *sql.Tx, modelID int64) (AdminCharityModel, error) {
	return scanAdminModel(tx.QueryRowContext(ctx, modelProjectionSQL, modelID))
}

const modelProjectionSQL = `SELECT cm.id,cm.provider,cm.model,cm.full_name,cm.enabled,cm.pricing_mode,
cm.request_user_price,cm.request_donor_reward,cm.uncached_user_price,cm.cache_write_user_price,
cm.cache_read_user_price,cm.output_user_price,cm.uncached_donor_reward,cm.cache_write_donor_reward,
cm.cache_read_donor_reward,cm.output_donor_reward,cm.discount_enabled,cm.discount_percent,
cm.discount_start_at,cm.discount_end_at,cm.flatten_tool_calls,cm.revision,cm.binding_revision,
(SELECT COUNT(*) FROM charity_model_bindings b WHERE b.charity_model_id=cm.id),
COALESCE(s.sample_count,0),COALESCE(s.success_count,0),cm.created_at,cm.updated_at
FROM charity_models cm LEFT JOIN charity_model_stats s ON s.model_id=cm.id WHERE cm.id=?`

type rowScanner interface{ Scan(...any) error }

func scanAdminModel(row rowScanner) (AdminCharityModel, error) {
	var value AdminCharityModel
	var id, revision, bindingRevision, bindingCount int64
	var enabled, discountEnabled, flatten int
	var mode string
	var requestUser, requestReward int64
	var user, reward [4]int64
	var start, end sql.NullInt64
	var samples, successes int
	err := row.Scan(&id, &value.Provider, &value.Model, &value.FullName, &enabled, &mode,
		&requestUser, &requestReward, &user[0], &user[1], &user[2], &user[3],
		&reward[0], &reward[1], &reward[2], &reward[3], &discountEnabled, &value.Discount.Percent,
		&start, &end, &flatten, &revision, &bindingRevision, &bindingCount, &samples, &successes,
		&value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminCharityModel{}, ErrNotFound
	}
	if err != nil {
		return AdminCharityModel{}, fmt.Errorf("charity routing: scan model: %w", err)
	}
	value.ID = strconv.FormatInt(id, 10)
	value.Enabled = enabled == 1
	value.FlattenToolCalls = flatten == 1
	value.Revision = strconv.FormatInt(revision, 10)
	value.BindingRevision = strconv.FormatInt(bindingRevision, 10)
	value.BindingCount = strconv.FormatInt(bindingCount, 10)
	value.Discount.Enabled = discountEnabled == 1
	if start.Valid {
		v := start.Int64
		value.Discount.StartAt = &v
	}
	if end.Valid {
		v := end.Int64
		value.Discount.EndAt = &v
	}
	value.Pricing.Mode = mode
	if mode == "per_request" {
		u, r := formatWireAmount(requestUser), formatWireAmount(requestReward)
		value.Pricing.UserPrice, value.Pricing.DonorReward = &u, &r
	} else if mode == "per_token" {
		value.Pricing.UserPrices = &AdminTokenPrices{
			UncachedInput: formatWireAmount(user[0]), CacheWriteInput: formatWireAmount(user[1]),
			CacheReadInput: formatWireAmount(user[2]), Output: formatWireAmount(user[3]),
		}
		value.Pricing.DonorRewards = &AdminTokenPrices{
			UncachedInput: formatWireAmount(reward[0]), CacheWriteInput: formatWireAmount(reward[1]),
			CacheReadInput: formatWireAmount(reward[2]), Output: formatWireAmount(reward[3]),
		}
	} else {
		return AdminCharityModel{}, ErrInvariant
	}
	value.RollingSuccess.SampleCount = strconv.Itoa(samples)
	value.RollingSuccess.SuccessCount = strconv.Itoa(successes)
	if samples > 0 {
		percent := formatBasisPoints((10000 * successes) / samples)
		value.RollingSuccess.Percent = &percent
	}
	return value, nil
}

func stewardModel(value AdminCharityModel) StewardCharityModel {
	pricing := StewardPricing{Mode: value.Pricing.Mode, UserPrice: copyString(value.Pricing.UserPrice), DonorReward: copyString(value.Pricing.DonorReward)}
	if value.Pricing.UserPrices != nil {
		pricing.UserPrices = &StewardTokenPrices{
			UncachedInput: value.Pricing.UserPrices.UncachedInput, CacheWriteInput: value.Pricing.UserPrices.CacheWriteInput,
			CacheReadInput: value.Pricing.UserPrices.CacheReadInput, Output: value.Pricing.UserPrices.Output,
		}
	}
	if value.Pricing.DonorRewards != nil {
		pricing.DonorRewards = &StewardTokenPrices{
			UncachedInput: value.Pricing.DonorRewards.UncachedInput, CacheWriteInput: value.Pricing.DonorRewards.CacheWriteInput,
			CacheReadInput: value.Pricing.DonorRewards.CacheReadInput, Output: value.Pricing.DonorRewards.Output,
		}
	}
	return StewardCharityModel{
		ID: value.ID, Provider: value.Provider, Model: value.Model, FullName: value.FullName,
		Enabled: value.Enabled, Pricing: pricing,
		Discount: StewardDiscount{Enabled: value.Discount.Enabled, Percent: value.Discount.Percent,
			StartAt: copyInt(value.Discount.StartAt), EndAt: copyInt(value.Discount.EndAt)},
		FlattenToolCalls: value.FlattenToolCalls, Revision: value.Revision,
		BindingRevision: value.BindingRevision, BindingCount: value.BindingCount,
		RollingSuccess: StewardRollingSuccess{SampleCount: value.RollingSuccess.SampleCount,
			SuccessCount: value.RollingSuccess.SuccessCount, Percent: copyString(value.RollingSuccess.Percent)},
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

type validatedPricing struct {
	requestUser, requestReward int64
	user, reward               [4]int64
}

func validateModelCreate(input ModelCreate) (validatedPricing, error) {
	if !validModelName(input.Provider) || !validModelName(input.Model) || !validDiscount(input.Discount) {
		return validatedPricing{}, ErrInvalidRequest
	}
	return validatePricing(input.Pricing)
}

func validateModelPatch(input ModelPatch) bool {
	if input.ExpectedRevision == "" || input.Provider == nil && input.Model == nil && input.Enabled == nil &&
		input.Pricing == nil && input.Discount == nil && input.FlattenToolCalls == nil {
		return false
	}
	if input.Provider != nil && !validModelName(*input.Provider) || input.Model != nil && !validModelName(*input.Model) ||
		input.Discount != nil && !validDiscountPatch(*input.Discount) {
		return false
	}
	return true
}

func validDiscountPatch(input DiscountPatchInput) bool {
	if input.Enabled == nil && input.Percent == nil && input.StartAt == nil && input.EndAt == nil {
		return false
	}
	return input.Percent == nil || *input.Percent >= 0 && *input.Percent <= 100
}

func nullIntPointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	out := value.Int64
	return &out
}

func validatePricing(input PricingInput) (validatedPricing, error) {
	var out validatedPricing
	switch input.Mode {
	case "per_request":
		if input.UserPrice == nil || input.DonorReward == nil || input.UserPrices != nil || input.DonorRewards != nil {
			return out, ErrInvalidRequest
		}
		var err error
		out.requestUser, err = parseWireAmount(*input.UserPrice)
		if err == nil {
			out.requestReward, err = parseWireAmount(*input.DonorReward)
		}
		return out, err
	case "per_token":
		if input.UserPrice != nil || input.DonorReward != nil || input.UserPrices == nil || input.DonorRewards == nil {
			return out, ErrInvalidRequest
		}
		user := []string{input.UserPrices.UncachedInput, input.UserPrices.CacheWriteInput, input.UserPrices.CacheReadInput, input.UserPrices.Output}
		reward := []string{input.DonorRewards.UncachedInput, input.DonorRewards.CacheWriteInput, input.DonorRewards.CacheReadInput, input.DonorRewards.Output}
		for index := range user {
			var err error
			out.user[index], err = parseWireAmount(user[index])
			if err == nil {
				out.reward[index], err = parseWireAmount(reward[index])
			}
			if err != nil {
				return validatedPricing{}, err
			}
		}
		return out, nil
	default:
		return out, ErrInvalidRequest
	}
}

func validDiscount(input DiscountInput) bool {
	return input.Percent >= 0 && input.Percent <= 100 &&
		(input.StartAt == nil || *input.StartAt >= 0 && *input.StartAt <= maxUnixSecond) &&
		(input.EndAt == nil || *input.EndAt >= 0 && *input.EndAt <= maxUnixSecond) &&
		(input.StartAt == nil || input.EndAt == nil || *input.EndAt >= *input.StartAt)
}

func validModelName(value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > maxNameRunes {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '/' || r == 0x7f {
			return false
		}
	}
	return true
}

func insertPolicyAudit(ctx context.Context, tx *sql.Tx, actorID int64, role string, modelID int64, oldValue, newValue bool, now int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO policy_audits(
actor_user_id,actor_role,resource_type,resource_id,policy,old_value,new_value,created_at)
VALUES(?,?,'charity_model',?,'flatten_tool_calls',?,?,?)`, actorID, role, modelID, boolInt(oldValue), boolInt(newValue), now); err != nil {
		return fmt.Errorf("charity routing: record policy audit: %w", err)
	}
	return nil
}

func (s *Service) beginRoleTx(ctx context.Context, role roleKind, actorUserID int64) (*sql.Tx, int64, error) {
	if s == nil || s.db == nil || nilDependency(s.roleAuth) {
		return nil, 0, ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("charity routing: begin role transaction: %w", err)
	}
	fail := func(err error) (*sql.Tx, int64, error) {
		_ = tx.Rollback()
		return nil, 0, err
	}
	switch role {
	case roleAdmin:
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&actorUserID); errors.Is(err, sql.ErrNoRows) {
			return fail(ErrForbidden)
		} else if err != nil {
			return fail(fmt.Errorf("charity routing: read singleton admin: %w", err))
		}
		if err := s.roleAuth.AuthorizeAdminMutation(ctx, tx, actorUserID); err != nil {
			return fail(mapAuthorization(err))
		}
	case roleSteward:
		if actorUserID <= 0 {
			return fail(ErrInvalidRequest)
		}
		if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorUserID); err != nil {
			return fail(mapAuthorization(err))
		}
	default:
		return fail(ErrInvalidRequest)
	}
	return tx, actorUserID, nil
}

func (s *Service) nowUnix() (int64, error) {
	if s == nil || s.now == nil {
		return 0, ErrUnavailable
	}
	value := s.now().Unix()
	if value < 0 || value > maxUnixSecond-idempotency.ReplayWindowSeconds {
		return 0, ErrUnavailable
	}
	return value, nil
}

func beginMutation(ctx context.Context, tx *sql.Tx, role roleKind, actorID int64, mutation resources.ControlMutation, now int64) (idempotency.Decision, error) {
	actor, err := idempotency.ActorScopeHash(string(role), strconv.FormatInt(actorID, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: mutation.Method, Route: mutation.Route,
		PathResourceIDs: mutation.PathIDs, Query: mutation.Query, Body: mutation.CanonicalBody,
	})
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: mutation.IdempotencyKey,
		RequestHash: digest, DecisionNow: now,
	})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return idempotency.Decision{}, ErrConflict
	}
	if err != nil {
		return idempotency.Decision{}, fmt.Errorf("charity routing: accept idempotency record: %w", err)
	}
	return decision, nil
}

func replay[T any](decision idempotency.Decision) (resources.MutationResult[T], error) {
	var value T
	if len(decision.ResponseBody) != 0 {
		if err := json.Unmarshal(decision.ResponseBody, &value); err != nil {
			return resources.MutationResult[T]{}, ErrInvariant
		}
	}
	return resources.MutationResult[T]{Value: value, Status: decision.HTTPStatus,
		Body: append([]byte(nil), decision.ResponseBody...), Replayed: true}, nil
}

func finishJSON[T any](ctx context.Context, tx *sql.Tx, decision idempotency.Decision, status int, value T) (resources.MutationResult[T], error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > idempotency.MaxResponseBytes {
		return resources.MutationResult[T]{}, ErrResourceLimit
	}
	if err := idempotency.Complete(ctx, tx, decision, status, body); err != nil {
		return resources.MutationResult[T]{}, fmt.Errorf("charity routing: complete idempotency record: %w", err)
	}
	return resources.MutationResult[T]{Value: value, Status: status, Body: body}, nil
}

func validMutation(input resources.ControlMutation, method, route string, ids ...int64) bool {
	if input.Method != method || input.Route != route || input.Query != "" || len(input.PathIDs) != len(ids) {
		return false
	}
	for index, id := range ids {
		if id <= 0 || input.PathIDs[index] != strconv.FormatInt(id, 10) {
			return false
		}
	}
	return true
}

func parsePositiveID(value string) (int64, error) {
	if value == "" || len(value) > 19 || len(value) > 1 && value[0] == '0' {
		return 0, ErrInvalidRequest
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return 0, ErrInvalidRequest
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, ErrInvalidRequest
	}
	return parsed, nil
}

func parseWireAmount(value string) (int64, error) {
	if value == "" || len(value) > 32 || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, ErrInvalidRequest
	}
	whole, fraction := value, ""
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		if strings.Contains(value[dot+1:], ".") {
			return 0, ErrInvalidRequest
		}
		whole, fraction = value[:dot], value[dot+1:]
		if fraction == "" || len(fraction) > 3 {
			return 0, ErrInvalidRequest
		}
	}
	if whole == "" || len(whole) > 1 && whole[0] == '0' || !digits(whole) || !digits(fraction) {
		return 0, ErrInvalidRequest
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, ErrInvalidRequest
	}
	for len(fraction) < 3 {
		fraction += "0"
	}
	f := int64(0)
	if fraction != "" {
		f, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, ErrInvalidRequest
		}
	}
	if w > (db.MaxMoneyMilli-f)/1000 {
		return 0, ErrInvalidRequest
	}
	return w*1000 + f, nil
}

func formatWireAmount(value int64) string {
	whole, fraction := value/1000, value%1000
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	text := strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(fraction+1000, 10)[1:]
	return strings.TrimRight(text, "0")
}

func formatBasisPoints(value int) string {
	whole, fraction := value/100, value%100
	if fraction == 0 {
		return strconv.Itoa(whole)
	}
	text := fmt.Sprintf("%d.%02d", whole, fraction)
	return strings.TrimRight(text, "0")
}

func digits(value string) bool {
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func nullableInt(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullableSQL(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func requireOne(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("charity routing: inspect transition: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func scanIDs(rows *sql.Rows) ([]int64, error) {
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("charity routing: scan identity: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("charity routing: iterate identities: %w", err)
	}
	return ids, nil
}

func finishTx(tx *sql.Tx, committed *bool) {
	if tx != nil && committed != nil && !*committed {
		_ = tx.Rollback()
	}
}

func commitTx(tx *sql.Tx, committed *bool) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("charity routing: commit transaction: %w", err)
	}
	*committed = true
	return nil
}

func classifyWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "unique constraint") || strings.Contains(text, "constraint failed") {
		return ErrConflict
	}
	return fmt.Errorf("charity routing: %s: %w", operation, err)
}

func mapAuthorization(err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "unauthorized") || strings.Contains(text, "authentication") {
		return ErrUnauthorized
	}
	return ErrForbidden
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func copyInt(value *int64) *int64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
