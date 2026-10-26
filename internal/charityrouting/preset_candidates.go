package charityrouting

import (
	"context"
	"database/sql"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Custom sources reuse mappings without acquiring the preset channel's identity.
// Mainstream sources retain the original exact primary-entry qualification.
func presetCandidatesQuery(donationID, now int64, q, state, channelID string, scope charityscope.Scope) (string, []any) {
	recommended := `EXISTS(SELECT 1 FROM donation_keys rd JOIN mainstream_channel_entries re
 ON re.channel_id=c.id AND re.connector_type=rd.connector_type AND re.canonical_base_url=rd.canonical_base_url
 WHERE rd.donation_id=? AND rd.ended_at IS NULL AND (rd.expires_at IS NULL OR rd.expires_at>?))`
	query := `SELECT ` + presetChannelColumns + `,` + recommended + ` AS recommended` + presetChannelFrom + `
 WHERE c.enabled=1 AND c.state='active' AND instr(lower(c.name),lower(?))>0
 AND (?='' OR c.id=?)
 AND EXISTS(SELECT 1 FROM channel_charity_binding_mappings pm WHERE pm.channel_id=c.id)
 AND EXISTS(SELECT 1 FROM donation_keys dk WHERE dk.donation_id=? AND dk.ended_at IS NULL
 AND (dk.expires_at IS NULL OR dk.expires_at>?) AND (dk.mainstream_channel_id IS NULL OR
 (dk.mainstream_channel_id=c.id AND dk.connector_type=e.connector_type AND dk.canonical_base_url=e.canonical_base_url)))`
	args := []any{donationID, now, q, channelID, channelID, donationID, now}
	if state != "all" {
		query += ` AND c.state=?`
		args = append(args, state)
	}
	if scope.Trainee {
		query += ` AND NOT EXISTS(SELECT 1 FROM channel_charity_binding_mappings pm JOIN charity_models cm ON cm.id=pm.charity_model_id WHERE pm.channel_id=c.id AND cm.is_mainstream=0)`
	}
	return query, args
}

func scanPresetCandidate(scan func(...any) error) (BindingPresetChannel, error) {
	var recommended bool
	channel, err := scanPresetChannel(func(values ...any) error {
		return scan(append(values, &recommended)...)
	})
	channel.Recommended = &recommended
	return channel, err
}

func requirePresetDonationTx(ctx context.Context, tx *sql.Tx, scope charityscope.Scope, donationID, now int64) (int64, error) {
	if err := scope.RequireDonation(ctx, tx, donationID, now); err != nil {
		return 0, managementScopeError(err)
	}
	return donationBindingRevision(ctx, tx, donationID)
}

func (s *Service) donationBindingPresets(ctx context.Context, role roleKind, actorID, donationID int64, q, state string, page pagination.Request) (Page[BindingPresetChannel], error) {
	out := Page[BindingPresetChannel]{Data: []BindingPresetChannel{}}
	if ctx == nil || donationID <= 0 || !page.Valid() || !utf8.ValidString(q) || utf8.RuneCountInString(q) > 128 || state != "active" && state != "retired" && state != "all" {
		return out, ErrInvalidRequest
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorID, 0, true, true)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if role != roleAdmin {
		for _, permission := range []string{"charity.presets", "charity.manage"} {
			if err := authz.RequireStewardPermission(ctx, tx, scope.ActorID, permission); err != nil {
				return out, err
			}
		}
	}
	now, err := s.nowUnix()
	if err != nil {
		return out, err
	}
	if _, err := requirePresetDonationTx(ctx, tx, scope, donationID, now); err != nil {
		return out, err
	}
	query, args := presetCandidatesQuery(donationID, now, q, state, "", scope)
	meta, offset, err := pageWindow(ctx, tx, query, args, page)
	if err != nil {
		return out, err
	}
	out.Pagination = &meta
	rows, err := tx.QueryContext(ctx, query+` ORDER BY recommended DESC,c.name,c.id LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		channel, err := scanPresetCandidate(rows.Scan)
		if err != nil {
			return out, err
		}
		out.Data = append(out.Data, channel)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
