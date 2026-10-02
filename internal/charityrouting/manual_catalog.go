package charityrouting

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"unicode"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const routeAdminManual = routeAdminKeyModels + "/manual"
const routeStewardManual = routeStewardKeyModels + "/manual"

type ManualCandidate struct {
	UpstreamModelID     string  `json:"upstream_model_id"`
	DisplayName         string  `json:"display_name"`
	Source              string  `json:"source"`
	Verified            bool    `json:"verified"`
	ManualEntryID       *string `json:"manual_entry_id"`
	ManualEntryRevision *string `json:"manual_entry_revision"`
}

type ManualCatalog struct {
	Entries               []ManualCandidate `json:"entries"`
	ManualCatalogRevision string            `json:"manual_catalog_revision"`
}

type ManualCatalogInput struct {
	Entries                       []string
	ExpectedManualCatalogRevision string
}

func validManualName(value string) bool {
	if !validUpstreamModel(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func readManualCandidatesTx(ctx context.Context, tx *sql.Tx, keyID int64, q string, page pagination.Request) ([]ManualCandidate, *pagination.Metadata, string, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT manual_catalog_revision FROM donation_keys WHERE id=?`, keyID).Scan(&revision); err != nil {
		return nil, nil, "", err
	}
	catalog := `(SELECT pc.normalized_model_id,pc.automatic_supports,pc.manual_supports+EXISTS(SELECT 1 FROM donation_key_manual_models dm WHERE dm.donation_key_id=? AND dm.normalized_model_id=pc.normalized_model_id) AS manual_supports FROM model_pair_catalog pc WHERE pc.endpoint_key_id=(SELECT endpoint_key_id FROM donation_keys WHERE id=?) AND (pc.automatic_supports>0 OR pc.manual_supports>0)
 UNION ALL SELECT dm.normalized_model_id,0,1 FROM donation_key_manual_models dm WHERE dm.donation_key_id=? AND NOT EXISTS(SELECT 1 FROM model_pair_catalog pc WHERE pc.endpoint_key_id=(SELECT endpoint_key_id FROM donation_keys WHERE id=?) AND pc.normalized_model_id=dm.normalized_model_id AND (pc.automatic_supports>0 OR pc.manual_supports>0)))`
	from := ` FROM ` + catalog + ` pc LEFT JOIN donation_key_manual_models dm ON dm.donation_key_id=? AND dm.normalized_model_id=pc.normalized_model_id`
	args := []any{keyID, keyID, keyID, keyID, keyID}
	if q != "" {
		from += ` WHERE pc.normalized_model_id LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(q)+"%")
	}
	selection := `SELECT pc.normalized_model_id,COALESCE(dm.display_name,pc.normalized_model_id),pc.automatic_supports,pc.manual_supports,dm.id,dm.revision` + from
	meta, offset, err := pageWindow(ctx, tx, selection, args, page)
	if err != nil {
		return nil, nil, "", err
	}
	rows, err := tx.QueryContext(ctx, selection+` ORDER BY pc.normalized_model_id LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return nil, nil, "", err
	}
	defer rows.Close()
	out := make([]ManualCandidate, 0)
	for rows.Next() {
		var item ManualCandidate
		var automatic, manual int64
		var id, rev sql.NullInt64
		if err = rows.Scan(&item.UpstreamModelID, &item.DisplayName, &automatic, &manual, &id, &rev); err != nil {
			return nil, nil, "", err
		}
		item.Source = "automatic"
		if automatic == 0 {
			item.Source = "manual"
		} else if manual > 0 {
			item.Source = "both"
		}
		item.Verified = automatic > 0
		if id.Valid {
			idText, revisionText := strconv.FormatInt(id.Int64, 10), strconv.FormatInt(rev.Int64, 10)
			item.ManualEntryID = &idText
			item.ManualEntryRevision = &revisionText
		}
		out = append(out, item)
	}
	return out, &meta, strconv.FormatInt(revision, 10), rows.Err()
}

func (s *Service) MutateManualCatalog(ctx context.Context, admin bool, actorID, donationID, keyID, entryID int64, mutation resources.ControlMutation, input ManualCatalogInput) (resources.MutationResult[ManualCatalog], error) {
	empty := resources.MutationResult[ManualCatalog]{}
	role := roleSteward
	route := routeStewardManual
	if admin {
		role = roleAdmin
		route = routeAdminManual
	}
	method := http.MethodPost
	ids := []int64{donationID, keyID}
	if entryID > 0 {
		method = http.MethodDelete
		route += "/{entryId}"
		ids = append(ids, entryID)
	}
	expected, err := parsePositiveID(input.ExpectedManualCatalogRevision)
	validated := mutation
	validated.Query = ""
	if s == nil || ctx == nil || entryID < 0 || err != nil || mutation.Query != charityscope.Query(ctx) || !validMutation(validated, method, route, ids...) {
		return empty, ErrInvalidRequest
	}
	if entryID == 0 {
		if len(input.Entries) < 1 || len(input.Entries) > 100 {
			return empty, ErrInvalidRequest
		}
		for _, entry := range input.Entries {
			if !validManualName(entry) {
				return empty, ErrInvalidRequest
			}
		}
	} else if len(input.Entries) != 0 {
		return empty, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorID, charityscope.ModelID(ctx), false, false)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	if err = scope.RequireKey(ctx, tx, donationID, keyID, now, false); err != nil {
		return empty, managementScopeError(err)
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id WHERE dk.id=? AND d.id=? AND d.status IN ('pending','approved') AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?) AND EXISTS(SELECT 1 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=m.endpoint_key_id AND e.user_id=d.user_id))`, keyID, donationID, now).Scan(&active); err != nil {
		return empty, err
	}
	if !active {
		return empty, ErrConflict
	}
	decision, err := beginMutation(ctx, tx, roleKind(scope.AuditRole(admin)), scope.ActorID, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[ManualCatalog](decision)
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT manual_catalog_revision FROM donation_keys WHERE id=? AND donation_id=?`, keyID, donationID).Scan(&revision); err != nil {
		return empty, err
	}
	if revision != expected {
		return empty, ErrConflict
	}
	changed := false
	if entryID == 0 {
		for _, entry := range input.Entries {
			result, err := tx.ExecContext(ctx, `INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,?,?,1,?,?) ON CONFLICT(donation_key_id,normalized_model_id) DO NOTHING`, keyID, entry, entry, now, now)
			if err != nil {
				return empty, err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return empty, err
			}
			changed = changed || n > 0
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM donation_key_manual_models WHERE donation_key_id=?`, keyID).Scan(&count); err != nil {
			return empty, err
		}
		if count > 1024 {
			return empty, ErrResourceLimit
		}
	} else {
		var name string
		if err = tx.QueryRowContext(ctx, `SELECT normalized_model_id FROM donation_key_manual_models WHERE id=? AND donation_key_id=?`, entryID, keyID).Scan(&name); err == sql.ErrNoRows {
			return empty, ErrNotFound
		} else if err != nil {
			return empty, err
		}
		var bound bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_model_bindings b WHERE b.donation_key_id=? AND b.upstream_model_id=? AND NOT EXISTS(SELECT 1 FROM model_pair_catalog pc WHERE pc.endpoint_key_id=b.endpoint_key_id AND pc.normalized_model_id=b.upstream_model_id AND (pc.automatic_supports>0 OR pc.manual_supports>0)))`, keyID, name).Scan(&bound); err != nil {
			return empty, err
		}
		if bound {
			return empty, ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM donation_key_manual_models WHERE id=? AND donation_key_id=?`, entryID, keyID); err != nil {
			return empty, err
		}
		changed = true
	}
	if changed {
		result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET manual_catalog_revision=manual_catalog_revision+1 WHERE id=? AND manual_catalog_revision=?`, keyID, expected)
		if err != nil {
			return empty, err
		}
		if err = requireOne(result); err != nil {
			return empty, err
		}
	}
	revisionText := strconv.FormatInt(revision, 10)
	if changed {
		revisionText = strconv.FormatInt(revision+1, 10)
	}
	entries := make([]ManualCandidate, 0, len(input.Entries))
	seen := make(map[string]bool, len(input.Entries))
	for _, name := range input.Entries {
		if seen[name] {
			continue
		}
		seen[name] = true
		var id, rev, automatic int64
		if err = tx.QueryRowContext(ctx, `SELECT dm.id,dm.revision,COALESCE(pc.automatic_supports,0) FROM donation_key_manual_models dm LEFT JOIN model_pair_catalog pc ON pc.endpoint_key_id=(SELECT endpoint_key_id FROM donation_keys WHERE id=?) AND pc.normalized_model_id=dm.normalized_model_id WHERE dm.donation_key_id=? AND dm.normalized_model_id=?`, keyID, keyID, name).Scan(&id, &rev, &automatic); err != nil {
			return empty, err
		}
		idText, revText := strconv.FormatInt(id, 10), strconv.FormatInt(rev, 10)
		source := "manual"
		if automatic > 0 {
			source = "both"
		}
		entries = append(entries, ManualCandidate{UpstreamModelID: name, DisplayName: name, Source: source, Verified: automatic > 0, ManualEntryID: &idText, ManualEntryRevision: &revText})
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, ManualCatalog{Entries: entries, ManualCatalogRevision: revisionText})
	if err != nil {
		return empty, err
	}
	if err = commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}
