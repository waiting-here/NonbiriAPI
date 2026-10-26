package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/charityaccess"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/modelname"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func (s *Service) createModelTx(ctx context.Context, tx *sql.Tx, role roleKind, scope charityscope.Scope, input ModelCreate, now int64) (AdminCharityModel, error) {
	_, recurring, err := normalizeRecurring(input.RecurringDiscount)
	if err != nil {
		return AdminCharityModel{}, err
	}
	policy := rolepolicy.Default()
	if input.RolePolicy != nil {
		policy = input.RolePolicy.Clone()
	}
	encodedPolicy, policyErr := policy.Canonical()
	if policyErr != nil {
		return AdminCharityModel{}, ErrInvalidRequest
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
	prices, err := validateModelCreate(input)
	if s == nil || ctx == nil || err != nil {
		return AdminCharityModel{}, ErrInvalidRequest
	}
	if role != roleAdmin && input.AffinityTTLSeconds != nil {
		return AdminCharityModel{}, ErrForbidden
	}
	mask := 63
	if input.AllowedLevels != nil {
		mask, err = charityaccess.Mask(input.AllowedLevels)
		if err != nil {
			return AdminCharityModel{}, ErrInvalidRequest
		}
	}
	description, err := charityaccess.NormalizeDescription(input.PublicDescription)
	if err != nil {
		return AdminCharityModel{}, ErrInvalidRequest
	}
	actorID := scope.ActorID
	fullName := modelname.CharityPrefix + input.Provider + "/" + input.Model
	result, err := tx.ExecContext(ctx, `INSERT INTO charity_models(
provider,model,full_name,enabled,pricing_mode,request_user_price,request_donor_reward,
uncached_user_price,cache_write_user_price,cache_read_user_price,output_user_price,
uncached_donor_reward,cache_write_donor_reward,cache_read_donor_reward,output_donor_reward,
discount_percent,discount_start_at,discount_end_at,discount_enabled,flatten_tool_calls,role_policy,
created_by_user_id,revision,binding_revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,0,?,?)`,
		input.Provider, input.Model, fullName, boolInt(input.Enabled), input.Pricing.Mode,
		prices.requestUser, prices.requestReward, prices.user[0], prices.user[1], prices.user[2], prices.user[3],
		prices.reward[0], prices.reward[1], prices.reward[2], prices.reward[3], input.Discount.Percent,
		input.Discount.StartAt, input.Discount.EndAt, boolInt(input.Discount.Enabled), boolInt(input.FlattenToolCalls),
		encodedPolicy, actorID, now, now)
	if err != nil {
		return AdminCharityModel{}, classifyWrite("create charity model", err)
	}
	modelID, err := result.LastInsertId()
	if err != nil || modelID <= 0 {
		return AdminCharityModel{}, ErrInvariant
	}
	encodedHeaders, _ := sessionheader.Encode(input.SessionHeaders)
	excluded, _ := openai.NormalizeExcludedRequestFields(input.ExcludedRequestFields)
	encodedExcluded, _ := json.Marshal(excluded)
	if _, err := tx.ExecContext(ctx, `UPDATE charity_models SET is_mainstream=?,excluded_request_fields=?,transport_rule=?,model_types=?,context_pricing=?,session_headers=?,recurring_discount=?,cloudflare_proxy_enabled=? WHERE id=?`, input.IsMainstream, string(encodedExcluded), input.TransportRule, input.ModelTypes, prices.contextPricing, encodedHeaders, recurring, input.CloudflareProxyEnabled, modelID); err != nil {
		return AdminCharityModel{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO charity_model_stats(model_id) VALUES(?)`, modelID); err != nil {
		return AdminCharityModel{}, fmt.Errorf("charity routing: initialize stats: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO charity_model_access(model_id, allowed_level_mask, public_description) VALUES(?, ?, ?)`, modelID, mask, description); err != nil {
		return AdminCharityModel{}, fmt.Errorf("charity routing: initialize access: %w", err)
	}
	if err := setModelTokenReserve(ctx, tx, modelID, input.TokenReserveCredits); err != nil {
		return AdminCharityModel{}, err
	}
	if err := setRoutingStrategy(ctx, tx, modelID, defaultRouteStrategy(input.RouteStrategy)); err != nil {
		return AdminCharityModel{}, err
	}
	if err := insertRoutingSettings(ctx, tx, modelID, input.AffinityTTLSeconds); err != nil {
		return AdminCharityModel{}, err
	}
	if input.FlattenToolCalls {
		if err := insertPolicyAudit(ctx, tx, actorID, scope.AuditRole(role == roleAdmin), modelID, false, true, now); err != nil {
			return AdminCharityModel{}, err
		}
	}
	defaultPolicy, _ := rolepolicy.Default().Canonical()
	if encodedPolicy != defaultPolicy {
		if err := rolepolicy.RecordAudit(ctx, tx, actorID, scope.AuditRole(role == roleAdmin), "charity_model", modelID, 0, 1, now); err != nil {
			return AdminCharityModel{}, err
		}
	}
	value, err := s.getModelWithCloudflareMode(ctx, tx, modelID, now)
	if err != nil {
		return AdminCharityModel{}, err
	}
	return value, nil
}

func (s *Service) patchModelTx(ctx context.Context, tx *sql.Tx, role roleKind, scope charityscope.Scope, modelID int64, input ModelPatch, now int64) (AdminCharityModel, error) {
	expected, err := parsePositiveID(input.ExpectedRevision)
	if err != nil || !validateModelPatch(input) {
		return AdminCharityModel{}, ErrInvalidRequest
	}
	if role != roleAdmin && input.AffinityTTLSeconds != nil {
		return AdminCharityModel{}, ErrForbidden
	}
	actorID := scope.ActorID
	current, err := readStoredModelTx(ctx, tx, modelID)
	if err != nil {
		return AdminCharityModel{}, err
	}
	if current.revision != expected {
		return AdminCharityModel{}, ErrConflict
	}
	updated := current
	if input.SessionHeaders != nil {
		updated.sessionHeaders, _ = sessionheader.Encode(*input.SessionHeaders)
	}
	if input.ModelTypes != nil {
		updated.modelTypes = input.ModelTypes.Clone()
	}
	if input.TransportRule != nil {
		updated.transportRule = *input.TransportRule
	}
	if input.IsMainstream != nil {
		updated.isMainstream = *input.IsMainstream
	}
	if input.ExcludedRequestFields != nil {
		excluded, _ := openai.NormalizeExcludedRequestFields(*input.ExcludedRequestFields)
		encoded, _ := json.Marshal(excluded)
		updated.excludedFields = string(encoded)
	}
	if input.AllowedLevels != nil {
		updated.allowedMask, err = charityaccess.Mask(*input.AllowedLevels)
		if err != nil {
			return AdminCharityModel{}, ErrInvalidRequest
		}
	}
	if input.PublicDescription != nil {
		updated.publicDescription, err = charityaccess.NormalizeDescription(*input.PublicDescription)
		if err != nil {
			return AdminCharityModel{}, ErrInvalidRequest
		}
	}
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
			return AdminCharityModel{}, ErrInvalidRequest
		}
		updated.mode = input.Pricing.Mode
		updated.requestUser, updated.requestReward = prices.requestUser, prices.requestReward
		updated.user, updated.reward = prices.user, prices.reward
		updated.contextPricing = prices.contextPricing
	}
	if !validPricingOperations(updated.mode, updated.modelTypes) {
		return AdminCharityModel{}, ErrInvalidRequest
	}
	if input.Discount != nil {
		if !validDiscountPatch(*input.Discount) {
			return AdminCharityModel{}, ErrInvalidRequest
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
			return AdminCharityModel{}, ErrInvalidRequest
		}
	}
	if input.RecurringDiscount != nil {
		_, updated.recurringDiscount, err = normalizeRecurring(input.RecurringDiscount)
		if err != nil {
			return AdminCharityModel{}, err
		}
	}
	if input.RolePolicy != nil {
		encoded, err := input.RolePolicy.Canonical()
		if err != nil {
			return AdminCharityModel{}, ErrInvalidRequest
		}
		updated.rolePolicy = encoded
	}
	if input.CloudflareProxyEnabled != nil {
		updated.cloudflareProxyEnabled = *input.CloudflareProxyEnabled
	}
	if input.FlattenToolCalls != nil {
		updated.flatten = boolInt(*input.FlattenToolCalls)
	}
	if !validModelName(updated.provider) || !validModelName(updated.model) {
		return AdminCharityModel{}, ErrInvalidRequest
	}
	if input.ExcludedRequestFields != nil && s.adaptations != nil {
		modelAdaptation, err := s.adaptations.LoadTx(ctx, tx, requestadaptation.Ref{Scope: requestadaptation.ScopeCharityModel, ID: modelID})
		if err != nil {
			return AdminCharityModel{}, routingAdaptationError(err)
		}
		defer modelAdaptation.Clear()
		excluded, err := decodeExcludedFields(updated.excludedFields)
		if err != nil {
			return AdminCharityModel{}, err
		}
		if err := s.validateModelAdaptationsTx(ctx, tx, modelID, excluded, modelAdaptation.Document); err != nil {
			return AdminCharityModel{}, err
		}
	}
	fullName := modelname.CharityPrefix + updated.provider + "/" + updated.model
	result, err := tx.ExecContext(ctx, `UPDATE charity_models SET
provider=?,model=?,full_name=?,enabled=?,pricing_mode=?,request_user_price=?,request_donor_reward=?,
uncached_user_price=?,cache_write_user_price=?,cache_read_user_price=?,output_user_price=?,
uncached_donor_reward=?,cache_write_donor_reward=?,cache_read_donor_reward=?,output_donor_reward=?,
discount_percent=?,discount_start_at=?,discount_end_at=?,discount_enabled=?,flatten_tool_calls=?,role_policy=?,
revision=revision+1,updated_at=? WHERE id=? AND revision=?`,
		updated.provider, updated.model, fullName, updated.enabled, updated.mode, updated.requestUser, updated.requestReward,
		updated.user[0], updated.user[1], updated.user[2], updated.user[3], updated.reward[0], updated.reward[1],
		updated.reward[2], updated.reward[3], updated.discountPercent, nullableSQL(updated.discountStart),
		nullableSQL(updated.discountEnd), updated.discountEnabled, updated.flatten, updated.rolePolicy, now, modelID, expected)
	if err != nil {
		return AdminCharityModel{}, classifyWrite("patch charity model", err)
	}
	if err := requireOne(result); err != nil {
		return AdminCharityModel{}, err
	}
	if current.flatten != updated.flatten {
		if err := insertPolicyAudit(ctx, tx, actorID, scope.AuditRole(role == roleAdmin), modelID, current.flatten == 1, updated.flatten == 1, now); err != nil {
			return AdminCharityModel{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE charity_models SET is_mainstream=?,excluded_request_fields=?,transport_rule=?,model_types=?,context_pricing=?,session_headers=?,recurring_discount=?,cloudflare_proxy_enabled=? WHERE id=?`, updated.isMainstream, updated.excludedFields, updated.transportRule, updated.modelTypes, updated.contextPricing, updated.sessionHeaders, updated.recurringDiscount, updated.cloudflareProxyEnabled, modelID); err != nil {
		return AdminCharityModel{}, err
	}
	if input.TokenReserveCredits != nil {
		if err := setModelTokenReserve(ctx, tx, modelID, *input.TokenReserveCredits); err != nil {
			return AdminCharityModel{}, err
		}
	}
	result, err = tx.ExecContext(ctx, `UPDATE charity_model_access SET allowed_level_mask=?,public_description=? WHERE model_id=?`, updated.allowedMask, updated.publicDescription, modelID)
	if err != nil {
		return AdminCharityModel{}, fmt.Errorf("charity routing: update model access: %w", err)
	}
	if err := requireOne(result); err != nil {
		return AdminCharityModel{}, err
	}
	strategyChanged := false
	if input.RouteStrategy != nil {
		previousStrategy, err := readRoutingStrategy(ctx, tx, modelID)
		if err != nil {
			return AdminCharityModel{}, err
		}
		strategyChanged = previousStrategy != *input.RouteStrategy
		if err := setRoutingStrategy(ctx, tx, modelID, *input.RouteStrategy); err != nil {
			return AdminCharityModel{}, err
		}
	}
	if err := updateRoutingSettings(ctx, tx, modelID, strategyChanged, input.AffinityTTLSeconds); err != nil {
		return AdminCharityModel{}, err
	}
	if updated.rolePolicy != current.rolePolicy {
		if err := rolepolicy.RecordAudit(ctx, tx, actorID, scope.AuditRole(role == roleAdmin), "charity_model", modelID, current.revision, current.revision+1, now); err != nil {
			return AdminCharityModel{}, err
		}
	}
	value, err := s.getModelWithCloudflareMode(ctx, tx, modelID, now)
	if err != nil {
		return AdminCharityModel{}, err
	}
	return value, nil
}
