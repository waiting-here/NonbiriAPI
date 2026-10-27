package charityrouting

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/charityaccess"
	"github.com/waiting-here/NonbiriAPI/internal/charitydiscount"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
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
	routeCapability               = "/api/charity/models"
	routeAdminModels              = "/admin/api/charity-models"
	routeAdminModel               = "/admin/api/charity-models/{id}"
	routeAdminCandidates          = "/admin/api/charity-models/{id}/binding-candidates"
	routeAdminBindingDonations    = "/admin/api/charity-models/{id}/binding-donations"
	routeAdminBindingKeys         = "/admin/api/charity-models/{id}/binding-donations/{donationId}/keys"
	routeAdminBindings            = "/admin/api/charity-models/{id}/bindings"
	routeAdminBindingBatch        = "/admin/api/charity-models/{id}/bindings/batch"
	routeAdminBindingReceipt      = "/admin/api/charity-models/{id}/bindings/batch-receipt"
	routeAdminBindingOrder        = "/admin/api/charity-models/{id}/bindings/order"
	routeAdminBinding             = "/admin/api/charity-models/{id}/bindings/{bindingId}"
	routeStewardModels            = "/api/steward/charity-models"
	routeStewardModel             = "/api/steward/charity-models/{id}"
	routeStewardCandidates        = "/api/steward/charity-models/{id}/binding-candidates"
	routeStewardBindingDonations  = "/api/steward/charity-models/{id}/binding-donations"
	routeStewardBindingKeys       = "/api/steward/charity-models/{id}/binding-donations/{donationId}/keys"
	routeStewardBindings          = "/api/steward/charity-models/{id}/bindings"
	routeStewardBindingBatch      = "/api/steward/charity-models/{id}/bindings/batch"
	routeStewardBindingReceipt    = "/api/steward/charity-models/{id}/bindings/batch-receipt"
	routeStewardBindingOrder      = "/api/steward/charity-models/{id}/bindings/order"
	routeStewardBinding           = "/api/steward/charity-models/{id}/bindings/{bindingId}"
	routeAdminModelAdaptation     = "/admin/api/charity-models/{id}/request-adaptation"
	routeAdminBindingAdaptation   = "/admin/api/charity-models/{id}/bindings/{bindingId}/request-adaptation"
	routeStewardModelAdaptation   = "/api/steward/charity-models/{id}/request-adaptation"
	routeStewardBindingAdaptation = "/api/steward/charity-models/{id}/bindings/{bindingId}/request-adaptation"
)

type Config struct {
	Connectors    *connector.Registry
	BaseURLs      resources.BaseURLValidator
	Store         *db.Store
	Adaptations   *requestadaptation.Store
	RoleAuth      RoleFinalTxAuthorizer
	DonationState DonationStateOwner
	CursorKeys    resources.CursorKeyDeriver
	Entropy       io.Reader
	Now           func() time.Time
}

type Service struct {
	connectors    *connector.Registry
	baseURLs      resources.BaseURLValidator
	db            *sql.DB
	readDB        *sql.DB
	adaptations   *requestadaptation.Store
	roleAuth      RoleFinalTxAuthorizer
	donationState DonationStateOwner
	cursorKeys    resources.CursorKeyDeriver
	entropy       io.Reader
	now           func() time.Time
}

func New(config Config) (*Service, error) {
	if config.Store == nil || config.Store.DB() == nil || config.Store.ReadDB() == nil || nilDependency(config.DonationState) {
		return nil, errors.New("charity routing: store and donation state owner are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if nilDependency(config.Entropy) {
		config.Entropy = cryptorand.Reader
	}
	return &Service{connectors: config.Connectors, baseURLs: config.BaseURLs, db: config.Store.DB(), readDB: config.Store.ReadDB(), adaptations: config.Adaptations, roleAuth: config.RoleAuth, donationState: config.DonationState,
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
	_, _, err := normalizeRecurring(input.RecurringDiscount)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	policy := rolepolicy.Default()
	if input.RolePolicy != nil {
		policy = input.RolePolicy.Clone()
	}
	_, policyErr := policy.Canonical()
	if policyErr != nil {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
	}
	if input.ModelTypes == nil {
		input.ModelTypes = modeltype.Default()
	}
	if input.TransportRule == "" {
		input.TransportRule = transportpolicy.Passthrough
	}
	if input.RouteStrategy == "" {
		input.RouteStrategy = RouteCacheBalanced
	}
	_, err = validateModelCreate(input)
	if s == nil || ctx == nil || err != nil {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
	}
	if role != roleAdmin && input.AffinityTTLSeconds != nil {
		return resources.MutationResult[AdminCharityModel]{}, ErrForbidden
	}
	if input.AllowedLevels != nil {
		_, err = charityaccess.Mask(input.AllowedLevels)
		if err != nil {
			return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
		}
	}
	_, err = charityaccess.NormalizeDescription(input.PublicDescription)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorUserID, 0, true, false)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	actorID := scope.ActorID
	if role != roleAdmin {
		if err := authz.RequireStewardPermission(ctx, tx, actorID, "charity.create"); err != nil {
			return resources.MutationResult[AdminCharityModel]{}, err
		}
	}
	if scope.Trainee && !input.IsMainstream {
		return resources.MutationResult[AdminCharityModel]{}, authz.ErrPermissionDenied
	}
	decision, err := beginMutation(ctx, tx, role, actorID, mutation, now)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return s.replayModel(decision)
	}
	value, err := s.createModelTx(ctx, tx, role, scope, input, now)
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
	_, err := parsePositiveID(input.ExpectedRevision)
	if s == nil || ctx == nil || modelID <= 0 || err != nil || !validateModelPatch(input) {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvalidRequest
	}
	if role != roleAdmin && input.AffinityTTLSeconds != nil {
		return resources.MutationResult[AdminCharityModel]{}, ErrForbidden
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, err
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorUserID, modelID, false, false)
	actorID := scope.ActorID
	if err == nil && role != roleAdmin {
		if permissionErr := authorizeModelPatch(ctx, tx, actorID, input); permissionErr != nil {
			tx.Rollback()
			return resources.MutationResult[AdminCharityModel]{}, permissionErr
		}
	}
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
		return s.replayModel(decision)
	}
	value, err := s.patchModelTx(ctx, tx, role, scope, modelID, input, now)
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
	tx, scope, err := s.beginManagementTx(ctx, role, actorUserID, 0, true, false)
	if err != nil {
		return resources.MutationResult[struct{}]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	actorID := scope.ActorID
	if role != roleAdmin {
		if err := authz.RequireStewardPermission(ctx, tx, actorID, "charity.delete"); err != nil {
			return resources.MutationResult[struct{}]{}, err
		}
	}
	if scope.Trainee {
		var mainstream bool
		err := tx.QueryRowContext(ctx, `SELECT is_mainstream FROM charity_models WHERE id=?`, modelID).Scan(&mainstream)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return resources.MutationResult[struct{}]{}, err
		}
		if err == nil && !mainstream {
			return resources.MutationResult[struct{}]{}, ErrNotFound
		}
	}
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
	tx, _, err := s.beginManagementTx(ctx, roleAdmin, 0, modelID, false, true)
	if err != nil {
		return AdminCharityModel{}, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return AdminCharityModel{}, err
	}
	return s.getModelWithCloudflareMode(ctx, tx, modelID, now)
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
	if _, err := s.managementScope(ctx, tx, roleSteward, actorUserID, modelID, false); err != nil {
		return StewardCharityModel{}, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return StewardCharityModel{}, err
	}
	value, err := s.getModelWithCloudflareMode(ctx, tx, modelID, now)
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
	scope, err := s.managementScope(ctx, tx, roleSteward, actorUserID, 0, true)
	if err != nil {
		return nil, 0, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, 0, err
	}
	items, next, err := s.listModelsQuery(ctx, tx, query, enabled, afterID, limit, now, scope.Trainee)
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
	tx, _, err := s.beginManagementTx(ctx, roleAdmin, 0, 0, true, true)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return nil, 0, err
	}
	return s.listModelsQuery(ctx, tx, query, enabled, afterID, limit, now)
}

type modelQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) listModelsQuery(ctx context.Context, queryer modelQueryer, query string, enabled *bool, afterID int64, limit int, now int64, trainee ...bool) ([]AdminCharityModel, int64, error) {
	statement := `SELECT id FROM charity_models WHERE id>?`
	if len(trainee) == 1 && trainee[0] {
		statement += ` AND is_mainstream=1`
	}
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
		item, err := scanAdminModel(queryer.QueryRowContext(ctx, modelProjectionSQL, id), now)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	s.attachModelCloudflareMode(items)
	return items, next, nil
}

type storedModel struct {
	cloudflareProxyEnabled           bool
	recurringDiscount                string
	sessionHeaders                   string
	contextPricing                   string
	modelTypes                       modeltype.Set
	transportRule                    transportpolicy.Rule
	rolePolicy                       string
	isMainstream                     bool
	excludedFields                   string
	allowedMask                      int
	publicDescription                string
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
discount_percent,discount_start_at,discount_end_at,discount_enabled,flatten_tool_calls,revision,binding_revision,
(SELECT allowed_level_mask FROM charity_model_access WHERE model_id=charity_models.id),
(SELECT public_description FROM charity_model_access WHERE model_id=charity_models.id),is_mainstream,excluded_request_fields,role_policy,transport_rule,model_types,context_pricing,session_headers,recurring_discount,cloudflare_proxy_enabled
FROM charity_models WHERE id=?`, modelID).Scan(&value.id, &value.provider, &value.model, &value.enabled, &value.mode,
		&value.requestUser, &value.requestReward, &value.user[0], &value.user[1], &value.user[2], &value.user[3],
		&value.reward[0], &value.reward[1], &value.reward[2], &value.reward[3], &value.discountPercent,
		&value.discountStart, &value.discountEnd, &value.discountEnabled, &value.flatten, &value.revision, &value.bindingRevision, &value.allowedMask, &value.publicDescription, &value.isMainstream, &value.excludedFields, &value.rolePolicy, &value.transportRule, &value.modelTypes, &value.contextPricing, &value.sessionHeaders, &value.recurringDiscount, &value.cloudflareProxyEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return storedModel{}, ErrNotFound
	}
	if err != nil {
		return storedModel{}, fmt.Errorf("charity routing: read stored model: %w", err)
	}
	if !value.transportRule.Valid() {
		return storedModel{}, ErrInvariant
	}
	if _, err := rolepolicy.Decode(value.rolePolicy); err != nil {
		return storedModel{}, ErrInvariant
	}
	return value, nil
}

func getAdminModelDB(ctx context.Context, database *sql.DB, modelID, at int64) (AdminCharityModel, error) {
	return scanAdminModel(database.QueryRowContext(ctx, modelProjectionSQL, modelID), at)
}

func getAdminModelTx(ctx context.Context, tx *sql.Tx, modelID, at int64) (AdminCharityModel, error) {
	return scanAdminModel(tx.QueryRowContext(ctx, modelProjectionSQL, modelID), at)
}

const modelProjectionSQL = `SELECT cm.id,cm.provider,cm.model,cm.full_name,cm.enabled,cm.pricing_mode,
cm.request_user_price,cm.request_donor_reward,cm.uncached_user_price,cm.cache_write_user_price,
cm.cache_read_user_price,cm.output_user_price,cm.uncached_donor_reward,cm.cache_write_donor_reward,
cm.cache_read_donor_reward,cm.output_donor_reward,cm.discount_enabled,cm.discount_percent,
cm.discount_start_at,cm.discount_end_at,cm.flatten_tool_calls,cm.revision,cm.binding_revision,
(SELECT COUNT(*) FROM charity_model_bindings b WHERE b.charity_model_id=cm.id),
COALESCE(s.sample_count,0),COALESCE(s.success_count,0),cm.created_at,cm.updated_at,
COALESCE((SELECT strategy FROM charity_model_routing WHERE model_id=cm.id),'expiry_weighted'),
(SELECT affinity_ttl_seconds FROM charity_routing_settings WHERE model_id=cm.id),
(SELECT allowed_level_mask FROM charity_model_access WHERE model_id=cm.id),
(SELECT public_description FROM charity_model_access WHERE model_id=cm.id),
(SELECT amount_milli FROM charity_model_token_reserves WHERE model_id=cm.id),cm.is_mainstream,cm.excluded_request_fields,cm.role_policy,cm.transport_rule,cm.model_types,cm.context_pricing,cm.session_headers,cm.recurring_discount,cm.cloudflare_proxy_enabled
FROM charity_models cm LEFT JOIN charity_model_stats s ON s.model_id=cm.id WHERE cm.id=?`

type rowScanner interface{ Scan(...any) error }

func scanAdminModel(row rowScanner, at int64) (AdminCharityModel, error) {
	var value AdminCharityModel
	var id, revision, bindingRevision, bindingCount int64
	var enabled, discountEnabled, flatten int
	var mode string
	var requestUser, requestReward int64
	var user, reward [4]int64
	var start, end, tokenReserve sql.NullInt64
	var affinityTTL sql.NullInt64
	var samples, successes int
	var mask int
	var excluded, encodedPolicy, contextPricing, sessionHeaders, recurring string
	err := row.Scan(&id, &value.Provider, &value.Model, &value.FullName, &enabled, &mode,
		&requestUser, &requestReward, &user[0], &user[1], &user[2], &user[3],
		&reward[0], &reward[1], &reward[2], &reward[3], &discountEnabled, &value.Discount.Percent,
		&start, &end, &flatten, &revision, &bindingRevision, &bindingCount, &samples, &successes,
		&value.CreatedAt, &value.UpdatedAt, &value.RouteStrategy, &affinityTTL, &mask, &value.PublicDescription, &tokenReserve, &value.IsMainstream, &excluded, &encodedPolicy, &value.TransportRule, &value.ModelTypes, &contextPricing, &sessionHeaders, &recurring, &value.CloudflareProxyEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminCharityModel{}, ErrNotFound
	}
	if err != nil {
		return AdminCharityModel{}, fmt.Errorf("charity routing: scan model: %w", err)
	}
	if !value.TransportRule.Valid() {
		return AdminCharityModel{}, ErrInvariant
	}
	value.RolePolicy, err = rolepolicy.Decode(encodedPolicy)
	if err != nil {
		return AdminCharityModel{}, ErrInvariant
	}
	value.ID = strconv.FormatInt(id, 10)
	value.ExcludedRequestFields, err = decodeExcludedFields(excluded)
	if err != nil {
		return AdminCharityModel{}, err
	}
	if tokenReserve.Valid {
		if tokenReserve.Int64 < 1 || tokenReserve.Int64 > db.MaxMoneyMilli {
			return AdminCharityModel{}, ErrInvariant
		}
		value.TokenReserveCredits = new(formatWireAmount(tokenReserve.Int64))
	}
	value.AllowedLevels, err = charityaccess.Levels(mask)
	if err != nil {
		return AdminCharityModel{}, ErrInvariant
	}
	if !validRouteStrategy(value.RouteStrategy) {
		return AdminCharityModel{}, ErrInvariant
	}
	if !affinityTTL.Valid || !validAffinityTTL(int(affinityTTL.Int64)) {
		return AdminCharityModel{}, ErrInvariant
	}
	value.AffinityTTLSeconds = int(affinityTTL.Int64)
	value.CloudflareProxyMode = "direct"
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
	if mode == "per_request" || mode == "per_search_unit" {
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
	value.SessionHeaders, err = sessionheader.Decode(sessionHeaders)
	if err != nil {
		return AdminCharityModel{}, ErrInvariant
	}
	if err := adminContextPricing(&value.Pricing, contextPricing); err != nil {
		return AdminCharityModel{}, err
	}
	value.RecurringDiscount, err = charitydiscount.Decode(recurring)
	if err != nil {
		return AdminCharityModel{}, ErrInvariant
	}
	if err := projectAdminDiscount(&value, at); err != nil {
		return AdminCharityModel{}, err
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
	stewardContextPricing(value.Pricing, &pricing)
	return StewardCharityModel{
		CloudflareProxyEnabled: value.CloudflareProxyEnabled, CloudflareProxyMode: value.CloudflareProxyMode,
		RecurringDiscount: value.RecurringDiscount.Clone(), EffectiveDiscount: value.EffectiveDiscount, ServerNow: value.ServerNow,
		SessionHeaders: append([]sessionheader.Config{}, value.SessionHeaders...),
		ModelTypes:     value.ModelTypes.Clone(), TransportRule: value.TransportRule, RolePolicy: value.RolePolicy.Clone(),
		IsMainstream: value.IsMainstream, ExcludedRequestFields: append([]string{}, value.ExcludedRequestFields...),
		TokenReserveCredits: copyString(value.TokenReserveCredits),
		AllowedLevels:       append([]int{}, value.AllowedLevels...), PublicDescription: value.PublicDescription,
		RouteStrategy:      defaultRouteStrategy(value.RouteStrategy),
		AffinityTTLSeconds: value.AffinityTTLSeconds,
		ID:                 value.ID, Provider: value.Provider, Model: value.Model, FullName: value.FullName,
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
	contextPricing             string
	requestUser, requestReward int64
	user, reward               [4]int64
}

func validateModelCreate(input ModelCreate) (validatedPricing, error) {
	if _, err := sessionheader.Normalize(input.SessionHeaders); err != nil {
		return validatedPricing{}, ErrInvalidRequest
	}
	if !input.TransportRule.Valid() || !input.ModelTypes.Valid() {
		return validatedPricing{}, ErrInvalidRequest
	}
	if _, err := openai.NormalizeExcludedRequestFields(input.ExcludedRequestFields); err != nil {
		return validatedPricing{}, ErrInvalidRequest
	}
	if !validModelTokenReserve(input.TokenReserveCredits) {
		return validatedPricing{}, ErrInvalidRequest
	}
	if !validRouteStrategy(defaultRouteStrategy(input.RouteStrategy)) {
		return validatedPricing{}, ErrInvalidRequest
	}
	if input.AffinityTTLSeconds != nil && !validAffinityTTL(*input.AffinityTTLSeconds) {
		return validatedPricing{}, ErrInvalidRequest
	}
	if !validModelName(input.Provider) || !validModelName(input.Model) || !validDiscount(input.Discount) {
		return validatedPricing{}, ErrInvalidRequest
	}
	if !validPricingOperations(input.Pricing.Mode, input.ModelTypes) {
		return validatedPricing{}, ErrInvalidRequest
	}
	return validatePricing(input.Pricing)
}

func validateModelPatch(input ModelPatch) bool {
	if input.SessionHeaders != nil {
		if _, err := sessionheader.Normalize(*input.SessionHeaders); err != nil {
			return false
		}
	}
	if input.ModelTypes != nil && !input.ModelTypes.Valid() {
		return false
	}
	if input.TransportRule != nil && !input.TransportRule.Valid() {
		return false
	}
	if input.ExcludedRequestFields != nil {
		if _, err := openai.NormalizeExcludedRequestFields(*input.ExcludedRequestFields); err != nil {
			return false
		}
	}
	if input.TokenReserveCredits != nil && !validModelTokenReserve(*input.TokenReserveCredits) {
		return false
	}
	if input.RouteStrategy != nil && !validRouteStrategy(*input.RouteStrategy) {
		return false
	}
	if input.AffinityTTLSeconds != nil && !validAffinityTTL(*input.AffinityTTLSeconds) {
		return false
	}
	if input.ExpectedRevision == "" || input.Provider == nil && input.Model == nil && input.Enabled == nil &&
		input.Pricing == nil && input.Discount == nil && input.RecurringDiscount == nil && input.FlattenToolCalls == nil && input.CloudflareProxyEnabled == nil && input.RouteStrategy == nil && input.AffinityTTLSeconds == nil && input.AllowedLevels == nil && input.PublicDescription == nil && input.TokenReserveCredits == nil && input.IsMainstream == nil && input.ExcludedRequestFields == nil && input.ModelTypes == nil && input.TransportRule == nil && input.RolePolicy == nil && input.SessionHeaders == nil {
		return false
	}
	if input.Provider != nil && !validModelName(*input.Provider) || input.Model != nil && !validModelName(*input.Model) ||
		input.Discount != nil && !validDiscountPatch(*input.Discount) {
		return false
	}
	return true
}

func validModelTokenReserve(value *string) bool {
	if value == nil {
		return true
	}
	amount, err := parseWireAmount(*value)
	return err == nil && amount > 0
}

func setModelTokenReserve(ctx context.Context, tx *sql.Tx, modelID int64, value *string) error {
	if value == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM charity_model_token_reserves WHERE model_id=?`, modelID)
		return err
	}
	amount, err := parseWireAmount(*value)
	if err != nil || amount < 1 {
		return ErrInvalidRequest
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO charity_model_token_reserves(model_id,amount_milli) VALUES(?,?)
ON CONFLICT(model_id) DO UPDATE SET amount_milli=excluded.amount_milli`, modelID, amount)
	if err != nil {
		return fmt.Errorf("charity routing: save model reservation: %w", err)
	}
	return nil
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
	context, err := validateContextPricing(input)
	if err != nil {
		return out, err
	}
	out.contextPricing = context.Encode()
	if len(out.contextPricing) > 8192 {
		return out, ErrInvalidRequest
	}
	switch input.Mode {
	case "per_request", "per_search_unit":
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
	actorUserID, err = s.authorizeRoleTx(ctx, tx, role, actorUserID)
	if err != nil {
		_ = tx.Rollback()
		return nil, 0, err
	}
	return tx, actorUserID, nil
}

func (s *Service) authorizeRoleTx(ctx context.Context, tx *sql.Tx, role roleKind, actorUserID int64) (int64, error) {
	switch role {
	case roleAdmin:
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&actorUserID); errors.Is(err, sql.ErrNoRows) {
			return 0, ErrForbidden
		} else if err != nil {
			return 0, fmt.Errorf("charity routing: read singleton admin: %w", err)
		}
		if err := s.roleAuth.AuthorizeAdminMutation(ctx, tx, actorUserID); err != nil {
			return 0, mapAuthorization(err)
		}
	case roleSteward:
		if actorUserID <= 0 {
			return 0, ErrInvalidRequest
		}
		if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorUserID); err != nil {
			return 0, mapAuthorization(err)
		}
	default:
		return 0, ErrInvalidRequest
	}
	return actorUserID, nil
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
	managementlog.ChangedFields(ctx, mutation.CanonicalBody)
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

func replayModel(decision idempotency.Decision) (resources.MutationResult[AdminCharityModel], error) {
	result, err := replay[AdminCharityModel](decision)
	if err != nil {
		return result, err
	}
	if result.Value.ExcludedRequestFields == nil {
		result.Value.ExcludedRequestFields = []string{}
	}
	if result.Value.AllowedLevels == nil {
		// Receipts predating level restrictions describe the former all-level
		// behavior. The stored immutable receipt and its existing fields stay intact.
		result.Value.AllowedLevels = []int{1, 2, 3, 4, 5, 6}
		result.Value.PublicDescription = ""
	}
	if result.Value.RecurringDiscount.TimeZone == "" {
		result.Value.RecurringDiscount = charitydiscount.Default()
	}
	if result.Value.EffectiveDiscount.Source == "" {
		if err := projectAdminDiscount(&result.Value, result.Value.UpdatedAt); err != nil {
			return resources.MutationResult[AdminCharityModel]{}, err
		}
	}
	result.Body, err = json.Marshal(result.Value)
	if err != nil {
		return resources.MutationResult[AdminCharityModel]{}, ErrInvariant
	}
	return result, nil
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
	if errors.Is(err, authz.ErrMFARequired) || errors.Is(err, authz.ErrPermissionDenied) {
		return err
	}
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

func validPricingOperations(mode string, operations modeltype.Set) bool {
	if mode != "per_search_unit" {
		return true
	}
	return len(operations) == 1 && string(operations[0]) == "rerank"
}
