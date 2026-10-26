// Package charityscope checks model targets and donated sources against live
// steward permissions. A context value is a selector, never authority.
package charityscope

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

var (
	ErrInvalid  = errors.New("charity scope: invalid selector")
	ErrNotFound = errors.New("charity scope: not found")
)

type Authorizer interface {
	AuthorizeAdminMutation(context.Context, *sql.Tx, int64) error
	AuthorizeStewardMutation(context.Context, *sql.Tx, int64) error
}

type traineeAuthorizer interface {
	AuthorizeTraineeMutation(context.Context, *sql.Tx, int64) error
}

type actorSource interface {
	SessionActor(context.Context) (authz.Actor, bool)
}

type selectorKey struct{}

func WithModel(ctx context.Context, modelID int64) context.Context {
	return context.WithValue(ctx, selectorKey{}, modelID)
}

func ModelID(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	id, _ := ctx.Value(selectorKey{}).(int64)
	return id
}

func Query(ctx context.Context) string {
	if id := ModelID(ctx); id > 0 {
		return "charity_model_id=" + strconv.FormatInt(id, 10)
	}
	return ""
}

// SelectRequest removes only the validated model selector from a private URL
// copy so existing endpoint-specific query parsers retain their strict allowlist.
// Mutations include Query(ctx) in their idempotency digest.
func SelectRequest(request *http.Request) (*http.Request, error) {
	if request == nil || request.URL == nil {
		return nil, ErrInvalid
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, ErrInvalid
	}
	entries, exists := values["charity_model_id"]
	if !exists {
		return request, nil
	}
	if len(entries) != 1 {
		return nil, ErrInvalid
	}
	id, err := strconv.ParseInt(entries[0], 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != entries[0] {
		return nil, ErrInvalid
	}
	values.Del("charity_model_id")
	result := request.Clone(WithModel(request.Context(), id))
	result.URL.RawQuery = values.Encode()
	return result, nil
}

type Scope struct {
	ActorID       int64
	ModelID       int64
	ModelRevision int64
	Trainee       bool
	Redact        bool
	ActorLevel    int
	DonationScope authz.DonationScope
}

func (scope Scope) AuditRole(admin bool) string {
	if admin {
		return "admin"
	}
	if scope.ActorLevel == 5 || scope.Trainee {
		return "trainee5"
	}
	return "level6"
}

// Authorize revalidates exact role/session in the caller's transaction.
// Source qualification is donation-wide; an optional model only selects a target.
func Authorize(ctx context.Context, tx *sql.Tx, authority Authorizer, admin bool, actorID, modelID int64, listing bool) (Scope, error) {
	result := Scope{ActorID: actorID, ModelID: modelID, DonationScope: authz.DonationScopeFull}
	if ctx == nil || tx == nil || authority == nil || modelID < 0 {
		return result, ErrInvalid
	}
	if admin {
		if actorID == 0 {
			if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&actorID); err != nil {
				return result, authz.ErrForbidden
			}
		}
		if err := authority.AuthorizeAdminMutation(ctx, tx, actorID); err != nil {
			return result, err
		}
		result.ActorID = actorID
	} else {
		var level, isAdmin int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level),is_admin FROM users WHERE id=?`, actorID).Scan(&level, &isAdmin); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return result, authz.ErrUnauthorized
			}
			return result, err
		}
		if isAdmin != 0 || level != 5 && level != 6 {
			return result, authz.ErrForbidden
		}
		result.ActorLevel = level
		if err := authz.RequireContextStewardPermission(ctx, tx, actorID); err != nil {
			return result, err
		}
		if level == 6 {
			if err := authority.AuthorizeStewardMutation(ctx, tx, actorID); err != nil {
				return result, err
			}
		} else {
			trainee, ok := authority.(traineeAuthorizer)
			if !ok {
				return result, authz.ErrForbidden
			}
			if err := trainee.AuthorizeTraineeMutation(ctx, tx, actorID); err != nil {
				return result, err
			}
			if _, err := SessionIdentity(ctx, authority, actorID); err != nil {
				return result, err
			}
			policy, err := authz.ReadStewardPermissions(ctx, tx, actorID)
			if err != nil {
				return result, err
			}
			result.Trainee = !policy.Permissions["charity.non_mainstream"]
			result.Redact = !policy.Permissions["donations.read_all"]
			result.DonationScope = policy.DonationScope
		}
	}
	if modelID > 0 {
		var mainstream bool
		err := tx.QueryRowContext(ctx, `SELECT revision,is_mainstream FROM charity_models WHERE id=?`, modelID).Scan(&result.ModelRevision, &mainstream)
		if errors.Is(err, sql.ErrNoRows) || err == nil && result.Trainee && !mainstream {
			return result, ErrNotFound
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func SessionIdentity(ctx context.Context, authority Authorizer, actorID int64) (string, error) {
	if caller, ok := authz.StewardCallerFromContext(ctx); ok && caller.UserID == actorID {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("caller:%d:%d", actorID, caller.Generation)))), nil
	}
	source, ok := authority.(actorSource)
	if !ok {
		return "", authz.ErrUnauthorized
	}
	actor, ok := source.SessionActor(ctx)
	if !ok || actor.UserID != actorID || actor.SessionTokenHash == "" || actor.SessionGeneration == "" ||
		actor.Kind != authz.ActorUserSession && actor.Kind != authz.ActorAdminSession {
		return "", authz.ErrUnauthorized
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", actor.Kind, actorID, actor.SessionGeneration, actor.SessionTokenHash)))), nil
}

func (scope Scope) CursorOwner(ctx context.Context, authority Authorizer, suffix string) (string, error) {
	identity, err := SessionIdentity(ctx, authority, scope.ActorID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d:%d:%s", identity, scope.ModelID, scope.ModelRevision, suffix), nil
}

func (scope Scope) RequireKey(ctx context.Context, tx *sql.Tx, donationID, keyID, now int64, adding bool) error {
	if scope.ActorLevel == 5 && scope.DonationScope == authz.DonationScopeNone {
		return authz.ErrPermissionDenied
	}
	if donationID <= 0 || keyID <= 0 {
		return ErrNotFound
	}
	query := `SELECT EXISTS(SELECT 1 FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id WHERE dk.id=? AND d.id=?`
	args := []any{keyID, donationID}
	if scope.RestrictedKeys() {
		predicate, values := scope.KeyPredicate(now, adding)
		query += " AND " + predicate
		args = append(args, values...)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, query+`)`, args...).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

type VisibleModel struct {
	ModelID      string `json:"model_id"`
	FullName     string `json:"full_name"`
	Enabled      bool   `json:"enabled"`
	BindingCount string `json:"binding_count"`
}

type Impact struct {
	BindingCount           string         `json:"binding_count"`
	VisibleModels          []VisibleModel `json:"visible_models"`
	VisibleModelsTruncated bool           `json:"visible_models_truncated"`
}

func (scope Scope) Impact(ctx context.Context, tx *sql.Tx, keyID int64) (Impact, error) {
	result := Impact{VisibleModels: []VisibleModel{}}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT binding_id) FROM charity_binding_targets WHERE donation_key_id=?`, keyID).Scan(&count); err != nil {
		return result, err
	}
	result.BindingCount = strconv.FormatInt(count, 10)
	rows, err := tx.QueryContext(ctx, `SELECT cm.id,cm.full_name,cm.enabled,COUNT(DISTINCT b.id) FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id
JOIN charity_models cm ON cm.id=b.charity_model_id WHERE t.donation_key_id=? AND (?=0 OR cm.is_mainstream=1)
GROUP BY cm.id,cm.full_name,cm.enabled ORDER BY cm.id LIMIT 21`, keyID, scope.Trainee)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item VisibleModel
		var id, bindings int64
		if err := rows.Scan(&id, &item.FullName, &item.Enabled, &bindings); err != nil {
			return result, err
		}
		if len(result.VisibleModels) == 20 {
			result.VisibleModelsTruncated = true
			break
		}
		item.ModelID, item.BindingCount = strconv.FormatInt(id, 10), strconv.FormatInt(bindings, 10)
		result.VisibleModels = append(result.VisibleModels, item)
	}
	return result, rows.Err()
}

// TaggedDonationSQL matches the entire donation when any canonical endpoint has a tag.
const TaggedDonationSQL = `EXISTS(SELECT 1 FROM donation_keys tagged_key WHERE tagged_key.donation_id=d.id AND EXISTS(SELECT 1 FROM effective_endpoint_tags tagged_endpoint WHERE tagged_endpoint.base_url=tagged_key.canonical_base_url))`

const unqualifiedScopeKeySQL = `scope_key.mainstream_channel_id IS NULL AND NOT EXISTS(SELECT 1 FROM effective_endpoint_tags scope_tag WHERE scope_tag.base_url=scope_key.canonical_base_url AND scope_tag.tag='mainstream')
 AND NOT EXISTS(SELECT 1 FROM charity_binding_targets scope_target JOIN charity_model_bindings scope_binding ON scope_binding.id=scope_target.binding_id JOIN charity_models scope_model ON scope_model.id=scope_binding.charity_model_id WHERE scope_target.donation_key_id=scope_key.id AND scope_model.is_mainstream=1)`

func (scope Scope) RestrictedKeys() bool {
	return scope.ActorLevel == 5 && scope.DonationScope != authz.DonationScopeFull
}

// DonationPredicate qualifies every unended, unexpired key together. Paused,
// failed and pending keys remain part of the resource authorization boundary.
// A saved mainstream channel or any mainstream model binding establishes source
// qualification independently of current approval and availability state.
func DonationPredicate(mode authz.DonationScope, now int64) (string, []any) {
	if mode == authz.DonationScopeFull {
		return "1", nil
	}
	if mode == authz.DonationScopeNone {
		return "0", nil
	}
	predicate := `(EXISTS(SELECT 1 FROM donation_keys scope_key WHERE scope_key.donation_id=d.id AND scope_key.ended_at IS NULL AND (scope_key.expires_at IS NULL OR scope_key.expires_at>?))
 AND NOT EXISTS(SELECT 1 FROM donation_keys scope_key WHERE scope_key.donation_id=d.id AND scope_key.ended_at IS NULL AND (scope_key.expires_at IS NULL OR scope_key.expires_at>?)
 AND ` + unqualifiedScopeKeySQL + `))`
	if mode == authz.DonationScopeEnhanced {
		predicate = "(" + predicate + " OR " + TaggedDonationSQL + ")"
	}
	return predicate, []any{now, now}
}

// TerminalReviewReceiptPredicate is only for an already-completed idempotent
// review. It preserves a successful terminal receipt when every historical key
// still qualifies, without granting ordinary access to ended donations. Tags and
// model bindings are current authority; saved mainstream source identities persist.
func TerminalReviewReceiptPredicate(mode authz.DonationScope) string {
	if mode == authz.DonationScopeNone {
		return "0"
	}
	predicate := `(EXISTS(SELECT 1 FROM donation_keys scope_key WHERE scope_key.donation_id=d.id)
 AND NOT EXISTS(SELECT 1 FROM donation_keys scope_key WHERE scope_key.donation_id=d.id AND ` + unqualifiedScopeKeySQL + `))`
	if mode == authz.DonationScopeEnhanced {
		predicate = "(" + predicate + " OR " + TaggedDonationSQL + ")"
	}
	return "(d.terminal_at IS NOT NULL AND " + predicate + ")"
}

// KeyPredicate is shared by point authorization and pre-pagination filtering.
// Adding and maintaining a source use the same donation-wide qualification.
func (scope Scope) KeyPredicate(now int64, adding bool) (string, []any) {
	if !scope.RestrictedKeys() {
		return "1", nil
	}
	return DonationPredicate(scope.DonationScope, now)
}

func (scope Scope) RequireDonation(ctx context.Context, tx *sql.Tx, donationID, now int64) error {
	if scope.ActorLevel == 5 && scope.DonationScope == authz.DonationScopeNone {
		return authz.ErrPermissionDenied
	}
	predicate, args := scope.KeyPredicate(now, false)
	args = append([]any{donationID}, args...)
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations d WHERE d.id=? AND `+predicate+`)`, args...).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}
