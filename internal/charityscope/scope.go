// Package charityscope keeps trainee management tied to a live session and
// an explicit mainstream model. A context value is a selector, never authority.
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
}

func (scope Scope) AuditRole(admin bool) string {
	if admin {
		return "admin"
	}
	if scope.Trainee {
		return "trainee5"
	}
	return "level6"
}

// Authorize revalidates exact role/session in the caller's transaction.
// A trainee may omit a model only when listing its mainstream models.
func Authorize(ctx context.Context, tx *sql.Tx, authority Authorizer, admin bool, actorID, modelID int64, listing bool) (Scope, error) {
	result := Scope{ActorID: actorID, ModelID: modelID}
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
			result.Trainee = true
			if modelID == 0 && !listing {
				return result, ErrNotFound
			}
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

// KeyPredicate uses only fixed repository aliases. Disabled and failed keys
// stay editable, while unbound mainstream candidates must retain a valid,
// approved and unexpired donor/physical membership.
func KeyPredicate() string {
	return `(EXISTS(SELECT 1 FROM charity_model_bindings scope_binding WHERE scope_binding.charity_model_id=? AND scope_binding.donation_key_id=dk.id)
OR (dk.mainstream_channel_id IS NOT NULL AND d.status='approved' AND d.user_id IS NOT NULL
AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?)
AND EXISTS(SELECT 1 FROM donation_key_memberships scope_membership
JOIN endpoint_keys scope_key ON scope_key.id=scope_membership.endpoint_key_id
JOIN endpoints scope_endpoint ON scope_endpoint.id=scope_key.endpoint_id
WHERE scope_membership.donation_key_id=dk.id AND scope_membership.donation_id=d.id
AND scope_membership.endpoint_key_id=dk.endpoint_key_id AND scope_endpoint.user_id=d.user_id)))`
}

func (scope Scope) RequireKey(ctx context.Context, tx *sql.Tx, donationID, keyID, now int64, adding bool) error {
	if donationID <= 0 || keyID <= 0 {
		return ErrNotFound
	}
	query := `SELECT EXISTS(SELECT 1 FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id WHERE dk.id=? AND d.id=?`
	args := []any{keyID, donationID}
	if scope.Trainee {
		query += ` AND ` + KeyPredicate()
		args = append(args, scope.ModelID, now)
		if adding {
			query += ` AND dk.mainstream_channel_id IS NOT NULL`
		}
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
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM charity_model_bindings WHERE donation_key_id=?`, keyID).Scan(&count); err != nil {
		return result, err
	}
	result.BindingCount = strconv.FormatInt(count, 10)
	rows, err := tx.QueryContext(ctx, `SELECT cm.id,cm.full_name,cm.enabled,COUNT(*) FROM charity_model_bindings b
JOIN charity_models cm ON cm.id=b.charity_model_id WHERE b.donation_key_id=? AND (?=0 OR cm.is_mainstream=1)
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
