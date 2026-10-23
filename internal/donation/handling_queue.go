package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type handlingActor struct {
	id                   int64
	hashKind, storedRole string
	trainee              bool
}

// Handling is a separate capability; it does not grant donation review or model access.
func (s *Service) beginHandlingTx(ctx context.Context, role reviewerRole, userID int64, readOnly bool) (*sql.Tx, handlingActor, error) {
	actor := handlingActor{id: userID, hashKind: string(role), storedRole: string(role)}
	if role == reviewerAdmin {
		tx, id, err := s.beginRoleTx(ctx, role, userID)
		actor.id = id
		return tx, actor, err
	}
	if s == nil || s.db == nil || ctx == nil || nilDependency(s.roleAuth) {
		return nil, actor, ErrUnavailable
	}
	if role != reviewerSteward || userID <= 0 {
		return nil, actor, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return nil, actor, err
	}
	rollback := func(err error) (*sql.Tx, handlingActor, error) {
		tx.Rollback()
		return nil, actor, err
	}
	var level, isAdmin int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level),is_admin FROM users WHERE id=?`, userID).Scan(&level, &isAdmin); errors.Is(err, sql.ErrNoRows) {
		return rollback(ErrUnauthorized)
	} else if err != nil {
		return rollback(fmt.Errorf("donation: read handling actor: %w", err))
	}
	if isAdmin != 0 || level != 5 && level != 6 {
		return rollback(ErrForbidden)
	}
	if level == 5 {
		authority, ok := s.roleAuth.(interface {
			AuthorizeTraineeMutation(context.Context, *sql.Tx, int64) error
		})
		if !ok {
			return rollback(ErrForbidden)
		}
		if err := authority.AuthorizeTraineeMutation(ctx, tx, userID); err != nil {
			return rollback(mapAuthorization(err))
		}
		actor.hashKind, actor.storedRole, actor.trainee = "trainee5", "level5", true
	} else if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, userID); err != nil {
		return rollback(mapAuthorization(err))
	}
	if _, err := charityscope.SessionIdentity(ctx, s.roleAuth, userID); err != nil {
		return rollback(mapAuthorization(err))
	}
	return tx, actor, nil
}

func requireApprovedHandlingTx(ctx context.Context, tx *sql.Tx, donationID, now int64) error {
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT `+manageableDonationSQL+` FROM donations d WHERE d.id=?`, now, donationID).Scan(&active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("donation: check handling eligibility: %w", err)
	}
	if !active {
		return ErrConflict
	}
	return nil
}

type HandlingQueueFilter struct {
	Handling   string
	DonationID int64
}

type HandlingSummary struct {
	DonationID    string           `json:"donation_id"`
	CreatedAt     int64            `json:"created_at"`
	KeyCount      string           `json:"key_count"`
	BoundKeyCount string           `json:"bound_key_count"`
	Handling      DonationHandling `json:"handling"`
}

func (s *Service) HandlingStewardPage(ctx context.Context, userID int64, filter HandlingQueueFilter, page pagination.Request) (Page[HandlingSummary], error) {
	var empty Page[HandlingSummary]
	if ctx == nil || userID <= 0 || filter.DonationID < 0 || !page.Valid() || filter.Handling != "" && filter.Handling != "pending" && filter.Handling != "processed" && filter.Handling != "all" {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.beginHandlingTx(ctx, reviewerSteward, userID, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	from := ` FROM donations d JOIN donation_handling h ON h.donation_id=d.id WHERE ` + manageableDonationSQL
	args := []any{now}
	if filter.Handling != "all" {
		handling := filter.Handling
		if handling == "" {
			handling = "pending"
		}
		from += ` AND h.state=?`
		args = append(args, handling)
	}
	if filter.DonationID > 0 {
		from += ` AND d.id=?`
		args = append(args, filter.DonationID)
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&count); err != nil {
		return empty, fmt.Errorf("donation: count handling queue: %w", err)
	}
	metadata, offset, err := page.Window(count)
	if err != nil {
		return empty, ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id,d.created_at,
(SELECT COUNT(*) FROM donation_keys dk WHERE dk.donation_id=d.id),
(SELECT COUNT(DISTINCT dk.id) FROM donation_keys dk JOIN charity_binding_targets b ON b.donation_key_id=dk.id WHERE dk.donation_id=d.id),
h.state,h.revision,h.processed_at,h.processed_by_role,h.closed_at,h.closed_reason`+from+` ORDER BY d.created_at DESC,d.id DESC LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return empty, fmt.Errorf("donation: read handling queue: %w", err)
	}
	items := make([]HandlingSummary, 0, page.Size)
	for rows.Next() {
		var item HandlingSummary
		var id, keys, bound, revision int64
		var role, reason string
		if err := rows.Scan(&id, &item.CreatedAt, &keys, &bound, &item.Handling.State, &revision, &item.Handling.ProcessedAt, &role, &item.Handling.ClosedAt, &reason); err != nil {
			rows.Close()
			return empty, err
		}
		item.DonationID, item.KeyCount, item.BoundKeyCount = strconv.FormatInt(id, 10), strconv.FormatInt(keys, 10), strconv.FormatInt(bound, 10)
		item.Handling = projectHandling(item.Handling, revision, role, reason)
		items = append(items, item)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return empty, readErr
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return Page[HandlingSummary]{Data: items, Pagination: &metadata}, nil
}

func (api *httpAPI) handlingQueueSteward(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := requestQuery(w, r)
	if !ok {
		return
	}
	page, err := parseNumberedQuery(values, "handling", "donation_id")
	if err != nil {
		writeDonationError(w, err)
		return
	}
	filter := HandlingQueueFilter{Handling: values.Get("handling")}
	if values.Has("handling") && filter.Handling == "" {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	if values.Has("donation_id") {
		filter.DonationID, err = parseCanonicalID(values.Get("donation_id"))
		if err != nil {
			writeDonationError(w, err)
			return
		}
	}
	out, err := api.service.HandlingStewardPage(r.Context(), p.UserID, filter, page)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}
