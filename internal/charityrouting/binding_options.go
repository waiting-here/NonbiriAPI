package charityrouting

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const routeAdminKeyBindingOptions = "/admin/api/donations/{id}/keys/{keyId}/binding-options"
const routeStewardKeyBindingOptions = "/api/steward/donations/{id}/keys/{keyId}/binding-options"

type KeyBindingOption struct {
	CharityModelID  string `json:"charity_model_id"`
	FullName        string `json:"full_name"`
	Enabled         bool   `json:"enabled"`
	BindingRevision string `json:"binding_revision"`
}

func (s *Service) keyBindingOptions(ctx context.Context, role roleKind, actorID, donationID, keyID int64, q string, page pagination.Request) (Page[KeyBindingOption], error) {
	out := Page[KeyBindingOption]{Data: []KeyBindingOption{}}
	if ctx == nil || donationID <= 0 || keyID <= 0 || !page.Valid() || !utf8.ValidString(q) || utf8.RuneCountInString(q) > 512 {
		return out, ErrInvalidRequest
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorID, 0, true, true)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return out, err
	}
	if !scope.Trainee {
		if err := scope.RequireKey(ctx, tx, donationID, keyID, now, false); err != nil {
			return out, managementScopeError(err)
		}
	}
	query := `SELECT cm.id,cm.full_name,cm.enabled,cm.binding_revision FROM charity_models cm WHERE instr(lower(cm.full_name),lower(?))>0`
	args := []any{q}
	if scope.Trainee {
		predicate := strings.Replace(charityscope.KeyPredicate(), "scope_binding.charity_model_id=?", "scope_binding.charity_model_id=cm.id", 1)
		query += ` AND cm.is_mainstream=1 AND EXISTS(SELECT 1 FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id WHERE dk.id=? AND d.id=? AND dk.mainstream_channel_id IS NOT NULL AND ` + predicate + `)`
		args = append(args, keyID, donationID, now)
	}
	meta, offset, err := pageWindow(ctx, tx, query, args, page)
	if err != nil {
		return out, err
	}
	out.Pagination = &meta
	rows, err := tx.QueryContext(ctx, query+` ORDER BY cm.full_name,cm.id LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item KeyBindingOption
		var id, revision int64
		if err := rows.Scan(&id, &item.FullName, &item.Enabled, &revision); err != nil {
			return out, err
		}
		item.CharityModelID = strconv.FormatInt(id, 10)
		item.BindingRevision = strconv.FormatInt(revision, 10)
		out.Data = append(out.Data, item)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
