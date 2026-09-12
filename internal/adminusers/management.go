package adminusers

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type managementRole uint8

const (
	roleAdmin managementRole = iota
	roleSteward
)

func (role managementRole) actorKind() string {
	if role == roleSteward {
		return "steward"
	}
	return "admin"
}

func (role managementRole) route(adminRoute string) string {
	if role == roleSteward {
		return "/api/steward" + strings.TrimPrefix(adminRoute, "/admin/api")
	}
	return adminRoute
}

func (s *Service) beginManagement(ctx context.Context, actorID int64, role managementRole, readOnly bool) (*sql.Tx, error) {
	if s == nil || s.database == nil || ctx == nil || actorID <= 0 {
		return nil, ErrUnauthorized
	}
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return nil, classifyDatabaseError("begin management transaction", err)
	}
	switch role {
	case roleAdmin:
		err = s.finalAuth.AuthorizeAdmin(ctx, tx, actorID)
	case roleSteward:
		err = s.finalAuth.AuthorizeStewardMutation(ctx, tx, actorID)
	default:
		err = ErrForbidden
	}
	if err != nil {
		tx.Rollback()
		return nil, classifyAuthorizationError(err)
	}
	return tx, nil
}

// The target check precedes replay for stewards. A cached response cannot
// authorize editing oneself or an account that has since become a steward.
func (s *Service) beginUserMutation(ctx context.Context, actorID, userID int64, role managementRole, control ControlMutation, now int64) (*sql.Tx, userRow, idempotency.Decision, error) {
	tx, err := s.beginManagement(ctx, actorID, role, false)
	if err != nil {
		return nil, userRow{}, idempotency.Decision{}, err
	}
	var row userRow
	if role == roleSteward {
		row, err = readUserRow(ctx, tx, userID)
		if err == nil && (userID == actorID || row.manualLevel.Valid && row.manualLevel.Int64 == 5) {
			err = ErrForbidden
		}
	}
	var decision idempotency.Decision
	if err == nil {
		decision, err = beginControlMutation(ctx, tx, actorID, role, control, now)
	}
	if err == nil && role == roleAdmin && decision.Kind != idempotency.Replay {
		row, err = readUserRow(ctx, tx, userID)
	}
	if err != nil {
		tx.Rollback()
		return nil, userRow{}, idempotency.Decision{}, err
	}
	return tx, row, decision, nil
}

func usersCursorOwner(query UserListQuery, role managementRole, actorID int64) string {
	filter := "any"
	if query.IsBanned != nil {
		filter = strconv.FormatBool(*query.IsBanned)
	}
	return filterOwner("users", role.actorKind(), strconv.FormatInt(actorID, 10), filter, query.Q, strconv.Itoa(query.Level))
}
