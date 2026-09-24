package donation

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const discoverySelectionScope = "donation-model-discovery-selection"
const routeAdminDiscoverySelection = "/admin/api/donation-keys/models/refresh/selection"
const routeStewardDiscoverySelection = "/api/steward/donation-keys/models/refresh/selection"

type DiscoveryRef struct {
	DonationID string `json:"donation_id"`
	KeyID      string `json:"key_id"`
}

type DiscoverySelection struct {
	Items      []DiscoveryRef `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

func (s *Service) selectDiscoveries(ctx context.Context, role reviewerRole, userID, donationID int64, token string) (DiscoverySelection, error) {
	empty := DiscoverySelection{Items: []DiscoveryRef{}}
	if donationID < 0 || len(token) > 4096 {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, scope, err := s.beginScopedTx(ctx, role, userID, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	if donationID != 0 {
		if err := requireManagedDonationTx(ctx, tx, role, donationID, now); err != nil {
			return empty, err
		}
	}
	if nilDependency(s.cursorKeys) {
		return empty, ErrUnavailable
	}
	key, err := s.cursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != 32 {
		clear(key)
		return empty, ErrUnavailable
	}
	defer clear(key)
	owner, err := scope.CursorOwner(ctx, s.roleAuth, fmt.Sprintf("%s:%d", role, donationID))
	if err != nil {
		return empty, scopeError(err)
	}
	started, maximum, after := now, int64(0), int64(0)
	if token == "" {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM donation_keys`).Scan(&maximum)
		if err != nil {
			return empty, err
		}
	} else {
		cursor, err := db.DecodePaginationCursorWithDerivedKey(key, token, discoverySelectionScope, owner, uint64(now))
		if err != nil || len(cursor.Atoms) != 3 {
			return empty, ErrInvalidRequest
		}
		for _, atom := range cursor.Atoms {
			if atom.Kind != db.CursorUint || atom.Uint > math.MaxInt64 {
				return empty, ErrInvalidRequest
			}
		}
		started, maximum, after = int64(cursor.Atoms[0].Uint), int64(cursor.Atoms[1].Uint), int64(cursor.Atoms[2].Uint)
		if started > now || cursor.Expiry != uint64(started)+3600 || after > maximum {
			return empty, ErrInvalidRequest
		}
	}
	// Budget candidate IDs before mutable eligibility filters, so sparse matches
	// still make bounded progress and newly inserted keys cannot extend a run.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM donation_keys WHERE id>? AND id<=? ORDER BY id LIMIT 100`, after, maximum)
	if err != nil {
		return empty, err
	}
	ids, err := scanIDs(rows)
	if err != nil || len(ids) == 0 {
		return empty, err
	}
	query := `SELECT d.id,dk.id` + discoveryTargetFromSQL + ` WHERE ` + discoveryEligibleSQL + ` AND dk.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
	args := []any{now, now}
	for _, id := range ids {
		args = append(args, id)
	}
	if donationID != 0 {
		query += ` AND d.id=?`
		args = append(args, donationID)
	}
	if scope.Trainee {
		query += ` AND ` + charityscope.KeyPredicate()
		args = append(args, scope.ModelID, now)
	}
	rows, err = tx.QueryContext(ctx, query+` ORDER BY dk.id`, args...)
	if err != nil {
		return empty, err
	}
	out, err := scanDiscoveryRefs(rows)
	if err != nil {
		return empty, err
	}
	after = ids[len(ids)-1]
	if after < maximum {
		next, err := db.EncodePaginationCursorWithDerivedKey(key, discoverySelectionScope, owner, uint64(started)+3600,
			[]db.CursorAtom{{Kind: db.CursorUint, Uint: uint64(started)}, {Kind: db.CursorUint, Uint: uint64(maximum)}, {Kind: db.CursorUint, Uint: uint64(after)}})
		if err != nil {
			return empty, ErrUnavailable
		}
		out.NextCursor = &next
	}
	return out, nil
}

func scanDiscoveryRefs(rows *sql.Rows) (DiscoverySelection, error) {
	defer rows.Close()
	out := DiscoverySelection{Items: []DiscoveryRef{}}
	for rows.Next() {
		var donationID, keyID int64
		if err := rows.Scan(&donationID, &keyID); err != nil {
			return out, err
		}
		out.Items = append(out.Items, DiscoveryRef{DonationID: strconv.FormatInt(donationID, 10), KeyID: strconv.FormatInt(keyID, 10)})
	}
	return out, rows.Err()
}

func (api *httpAPI) discoverySelectionAdmin(w http.ResponseWriter, req *http.Request) {
	api.discoverySelectionHTTP(w, req, reviewerAdmin, 0)
}

func (api *httpAPI) discoverySelectionSteward(w http.ResponseWriter, req *http.Request, principal UserPrincipal) {
	api.discoverySelectionHTTP(w, req, reviewerSteward, principal.UserID)
}

func (api *httpAPI) discoverySelectionHTTP(w http.ResponseWriter, req *http.Request, role reviewerRole, userID int64) {
	if !requireEmptyQuery(w, req) {
		return
	}
	var input struct {
		DonationID nullableField[string] `json:"donation_id"`
		Cursor     nullableField[string] `json:"cursor"`
	}
	if !decodeStrictObject(w, req, &input) {
		return
	}
	if !input.DonationID.Set || !input.Cursor.Set {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	var donationID int64
	var err error
	if input.DonationID.Value != nil {
		donationID, err = parseCanonicalID(*input.DonationID.Value)
		if err != nil {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
	}
	cursor := ""
	if input.Cursor.Value != nil {
		cursor = *input.Cursor.Value
		if cursor == "" {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
	}
	out, err := api.service.selectDiscoveries(req.Context(), role, userID, donationID, cursor)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}
