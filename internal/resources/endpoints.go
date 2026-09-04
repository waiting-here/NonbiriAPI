package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const (
	routeEndpoints         = "/api/endpoints"
	routeEndpoint          = "/api/endpoints/{id}"
	routeEndpointKeys      = "/api/endpoints/{id}/keys"
	routeEndpointKey       = "/api/endpoints/{id}/keys/{keyId}"
	maxEndpointSecretBytes = 64 * 1024
)

func validateEndpointSecretPlaintext(value []byte) bool {
	if len(value) == 0 || len(value) > maxEndpointSecretBytes || !utf8.Valid(value) {
		return false
	}
	for remaining := value; len(remaining) > 0; {
		runeValue, size := utf8.DecodeRune(remaining)
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
		remaining = remaining[size:]
	}
	return true
}

type endpointRow struct {
	id                        int64
	connectorType             string
	baseURL                   string
	note                      string
	enabled                   int
	revision                  int64
	keyCount                  int64
	createdAt                 int64
	updatedAt                 int64
	mainstreamChannelID       sql.NullString
	mainstreamChannelRevision sql.NullInt64
	mainstreamChannelName     sql.NullString
	mainstreamChannelCategory sql.NullString
}

func (row endpointRow) dto() (Endpoint, error) {
	id, err := decimalID(row.id)
	if err != nil || row.enabled < 0 || row.enabled > 1 || row.revision < 1 || row.keyCount < 0 {
		return Endpoint{}, ErrUnavailable
	}
	var origin EndpointOrigin
	fields := []bool{row.mainstreamChannelID.Valid, row.mainstreamChannelRevision.Valid, row.mainstreamChannelName.Valid, row.mainstreamChannelCategory.Valid}
	if fields[0] || fields[1] || fields[2] || fields[3] {
		if !(fields[0] && fields[1] && fields[2] && fields[3]) || !validMainstreamChannelID(row.mainstreamChannelID.String) ||
			row.mainstreamChannelRevision.Int64 < 1 || !validateExactText(row.mainstreamChannelName.String, 1, 128) ||
			!validMainstreamChannelCategory(row.mainstreamChannelCategory.String) {
			return Endpoint{}, ErrUnavailable
		}
		origin = EndpointOrigin{Kind: "mainstream", ChannelID: row.mainstreamChannelID.String, Name: row.mainstreamChannelName.String}
	} else {
		origin = EndpointOrigin{Kind: "custom"}
	}
	return Endpoint{
		ID: id, ConnectorType: row.connectorType, BaseURL: row.baseURL, Origin: origin, Note: row.note,
		Enabled: row.enabled == 1, Revision: strconv.FormatInt(row.revision, 10),
		KeyCount: strconv.FormatInt(row.keyCount, 10), CreatedAt: row.createdAt, UpdatedAt: row.updatedAt,
	}, nil
}

func scanEndpoint(scanner interface{ Scan(...any) error }) (Endpoint, error) {
	var row endpointRow
	if err := scanner.Scan(&row.id, &row.connectorType, &row.baseURL, &row.note, &row.enabled,
		&row.revision, &row.keyCount, &row.createdAt, &row.updatedAt,
		&row.mainstreamChannelID, &row.mainstreamChannelRevision, &row.mainstreamChannelName, &row.mainstreamChannelCategory); err != nil {
		return Endpoint{}, err
	}
	return row.dto()
}

func (r *Repository) ListEndpoints(ctx context.Context, userID int64, limit int, cursor string) (Page[Endpoint], error) {
	limit = normalizePageLimit(limit)
	if r == nil || userID <= 0 || !validPageLimit(limit) {
		return Page[Endpoint]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return Page[Endpoint]{}, err
	}
	var afterUpdated, afterID uint64
	if cursor != "" {
		atoms, err := r.cursors.decode(cursor, "user-endpoints", strconv.FormatInt(userID, 10), uint64(now), db.CursorUint, db.CursorUint)
		if err != nil {
			return Page[Endpoint]{}, err
		}
		afterUpdated, afterID = atoms[0].Uint, atoms[1].Uint
		if afterUpdated > uint64(maxUnixSecond) || afterID == 0 || afterID > uint64(^uint64(0)>>1) {
			return Page[Endpoint]{}, ErrInvalidRequest
		}
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT e.id,e.connector_type,e.base_url,e.note,e.enabled,e.revision,
       (SELECT count(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id),
       e.created_at,e.updated_at,e.mainstream_channel_id,e.mainstream_channel_revision,
       e.mainstream_channel_name,e.mainstream_channel_category
FROM endpoints e
WHERE e.user_id=?
  AND (?=0 OR e.updated_at<? OR (e.updated_at=? AND e.id<?))
ORDER BY e.updated_at DESC,e.id DESC
LIMIT ?`, userID, afterID, afterUpdated, afterUpdated, afterID, limit+1)
	if err != nil {
		return Page[Endpoint]{}, fmt.Errorf("resources: list endpoints: %w", err)
	}
	defer rows.Close()
	items := make([]Endpoint, 0, limit+1)
	for rows.Next() {
		item, err := scanEndpoint(rows)
		if err != nil {
			return Page[Endpoint]{}, fmt.Errorf("resources: scan endpoint: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[Endpoint]{}, fmt.Errorf("resources: list endpoints: %w", err)
	}
	page := Page[Endpoint]{Data: items}
	if len(items) > limit {
		last := items[limit-1]
		lastID, _ := parseDecimalID(last.ID)
		next, err := r.cursors.encode("user-endpoints", strconv.FormatInt(userID, 10), uint64(now+cursorLifetime), []db.CursorAtom{
			{Kind: db.CursorUint, Uint: uint64(last.UpdatedAt)}, {Kind: db.CursorUint, Uint: uint64(lastID)},
		})
		if err != nil {
			return Page[Endpoint]{}, err
		}
		page.Data = items[:limit]
		page.NextCursor = &next
	}
	return page, nil
}

func (r *Repository) GetEndpoint(ctx context.Context, userID, endpointID int64) (Endpoint, error) {
	if r == nil || userID <= 0 || endpointID <= 0 {
		return Endpoint{}, ErrNotFound
	}
	item, err := scanEndpoint(r.db.QueryRowContext(ctx, `
SELECT e.id,e.connector_type,e.base_url,e.note,e.enabled,e.revision,
       (SELECT count(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id),
       e.created_at,e.updated_at,e.mainstream_channel_id,e.mainstream_channel_revision,
       e.mainstream_channel_name,e.mainstream_channel_category
FROM endpoints e WHERE e.id=? AND e.user_id=?`, endpointID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}
	if err != nil {
		return Endpoint{}, fmt.Errorf("resources: get endpoint: %w", err)
	}
	return item, nil
}

func (r *Repository) CreateEndpoint(ctx context.Context, userID int64, mutation ControlMutation, input CreateEndpointInput) (MutationResult[Endpoint], error) {
	if r == nil || userID <= 0 || mutation.Route != routeEndpoints || mutation.Method != http.MethodPost || !mutationPathIDs(mutation) || mutation.Query != "" ||
		!validateNote(input.Note) || (input.Source != "custom" && input.Source != "mainstream") {
		return MutationResult[Endpoint]{}, ErrInvalidRequest
	}
	canonicalURL := ""
	connectorType := input.ConnectorType
	if input.Source == "custom" {
		if input.ChannelID != "" || input.ConnectorType == "" || input.BaseURL == "" {
			return MutationResult[Endpoint]{}, ErrInvalidRequest
		}
		validatedType, err := r.connectors.MustValidate(connectorcontract.Type(input.ConnectorType))
		if err != nil || string(validatedType) != input.ConnectorType {
			return MutationResult[Endpoint]{}, ErrInvalidRequest
		}
		canonicalURL, err = r.baseURLs.ValidateBaseURL(input.BaseURL)
		if err != nil || canonicalURL == "" || len(canonicalURL) > 4096 {
			return MutationResult[Endpoint]{}, ErrInvalidRequest
		}
	} else if input.ChannelID == "" || input.ConnectorType != "" || input.BaseURL != "" || !validMainstreamChannelID(input.ChannelID) {
		return MutationResult[Endpoint]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[Endpoint](decision)
	}
	var channelID any
	var channelRevision any
	var channelName any
	var channelCategory any
	if input.Source == "mainstream" {
		row, err := getMainstreamChannelSnapshotTx(ctx, tx, input.ChannelID)
		if err != nil {
			return MutationResult[Endpoint]{}, err
		}
		item, err := row.dto()
		if err != nil {
			return MutationResult[Endpoint]{}, ErrUnavailable
		}
		if err := r.validateMainstreamChannelDTO(item); err != nil {
			return MutationResult[Endpoint]{}, err
		}
		if item.State != mainstreamChannelStateActive || !item.Enabled {
			return MutationResult[Endpoint]{}, ErrConflict
		}
		canonicalURL = item.BaseURL
		connectorType = item.ConnectorType
		channelID, channelRevision, channelName, channelCategory = item.ID, row.revision, item.Name, item.Category
	}
	var endpointLimit sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT endpoint_limit FROM users WHERE id=? AND is_admin=0`, userID).Scan(&endpointLimit); errors.Is(err, sql.ErrNoRows) {
		return MutationResult[Endpoint]{}, ErrNotFound
	} else if err != nil {
		return MutationResult[Endpoint]{}, fmt.Errorf("resources: read endpoint limit: %w", err)
	}
	globalLimit, err := readSiteLimitTx(ctx, tx, "default_endpoint_limit", 0, 10000)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	effectiveLimit := globalLimit
	if endpointLimit.Valid {
		effectiveLimit = endpointLimit.Int64
	}
	count, err := countTx(ctx, tx, `SELECT count(*) FROM endpoints WHERE user_id=?`, userID)
	if err != nil {
		return MutationResult[Endpoint]{}, fmt.Errorf("resources: count endpoints: %w", err)
	}
	if count >= effectiveLimit {
		return MutationResult[Endpoint]{}, ErrResourceLimit
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,mainstream_channel_category,created_at,updated_at)
VALUES(?,?,?,?,?,1,?,?,?,?,?,?)`, userID, connectorType, canonicalURL, input.Note, boolInt(input.Enabled), channelID, channelRevision, channelName, channelCategory, now, now)
	if err != nil {
		return MutationResult[Endpoint]{}, fmt.Errorf("resources: create endpoint: %w", err)
	}
	endpointID, err := result.LastInsertId()
	if err != nil {
		return MutationResult[Endpoint]{}, fmt.Errorf("resources: create endpoint identity: %w", err)
	}
	item, err := getEndpointTx(ctx, tx, userID, endpointID)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusCreated, item)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[Endpoint]{}, err
	}
	return out, nil
}

func (r *Repository) PatchEndpoint(ctx context.Context, userID, endpointID int64, mutation ControlMutation, input PatchEndpointInput) (MutationResult[Endpoint], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || mutation.Route != routeEndpoint || mutation.Method != http.MethodPatch || !mutationPathIDs(mutation, endpointID) || mutation.Query != "" ||
		(input.Note == nil && input.Enabled == nil) || input.ExpectedRevision < 1 || (input.Note != nil && !validateNote(*input.Note)) {
		return MutationResult[Endpoint]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[Endpoint](decision)
	}
	current, err := getEndpointTx(ctx, tx, userID, endpointID)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	currentRevision, _ := strconv.ParseInt(current.Revision, 10, 64)
	if currentRevision != input.ExpectedRevision || currentRevision == int64(^uint64(0)>>1) {
		return MutationResult[Endpoint]{}, ErrConflict
	}
	locked, err := endpointLockedTx(ctx, tx, endpointID)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	if locked {
		return MutationResult[Endpoint]{}, ErrResourceLocked
	}
	note, enabled := current.Note, current.Enabled
	if input.Note != nil {
		note = *input.Note
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if !current.Enabled && enabled {
		incompatible, err := endpointEnableConflictsWithFlattenTx(ctx, tx, userID, endpointID)
		if err != nil {
			return MutationResult[Endpoint]{}, err
		}
		if incompatible {
			return MutationResult[Endpoint]{}, ErrConflict
		}
	}
	var affectedModels []bindingRevisionTarget
	if current.Enabled != enabled {
		keyIDs, err := verifiedEndpointKeyIDsTx(ctx, tx, userID, endpointID)
		if err != nil {
			return MutationResult[Endpoint]{}, err
		}
		affectedModels, err = bindingRevisionTargetsForKeysTx(ctx, tx, userID, keyIDs)
		if err != nil {
			return MutationResult[Endpoint]{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE endpoints SET note=?,enabled=?,revision=revision+1,updated_at=?
WHERE id=? AND user_id=? AND revision=?`, note, boolInt(enabled), now, endpointID, userID, input.ExpectedRevision)
	if err != nil {
		return MutationResult[Endpoint]{}, fmt.Errorf("resources: patch endpoint: %w", err)
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return MutationResult[Endpoint]{}, ErrConflict
	}
	if err := r.reconcileRoutingTargetsTx(ctx, tx, userID, affectedModels); err != nil {
		return MutationResult[Endpoint]{}, err
	}
	item, err := getEndpointTx(ctx, tx, userID, endpointID)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, item)
	if err != nil {
		return MutationResult[Endpoint]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[Endpoint]{}, err
	}
	return out, nil
}

func (r *Repository) DeleteEndpoint(ctx context.Context, userID, endpointID int64, mutation ControlMutation, expectedRevision int64) (MutationResult[struct{}], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || expectedRevision < 1 || mutation.Route != routeEndpoint || mutation.Method != http.MethodDelete || !mutationPathIDs(mutation, endpointID) || mutation.Query != "" {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[struct{}](decision)
	}
	current, err := getEndpointTx(ctx, tx, userID, endpointID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	currentRevision, _ := strconv.ParseInt(current.Revision, 10, 64)
	if currentRevision != expectedRevision {
		return MutationResult[struct{}]{}, ErrConflict
	}
	locked, err := endpointLockedTx(ctx, tx, endpointID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if locked {
		return MutationResult[struct{}]{}, ErrResourceLocked
	}
	refs, err := endpointSecretRefsTx(ctx, tx, endpointID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	keyIDs, err := verifiedEndpointKeyIDsTx(ctx, tx, userID, endpointID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	models, err := bindingRevisionTargetsForKeysTx(ctx, tx, userID, keyIDs)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := validateBindingRevisionTargets(models); err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := r.projection.PrepareEndpointDeletion(ctx, tx, userID, endpointID, now); err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: prepare endpoint projection deletion: %w", err)
	}
	if err := r.keyDeletion.PrepareEndpointKeyDeletion(ctx, tx, userID, keyIDs, now); err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: prepare endpoint key deletion: %w", err)
	}
	if err := advanceBindingRevisionsForDeletionTx(ctx, tx, userID, models, now); err != nil {
		return MutationResult[struct{}]{}, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM endpoints WHERE id=? AND user_id=? AND revision=?`, endpointID, userID, expectedRevision)
	if err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: delete endpoint: %w", err)
	}
	deleted, _ := result.RowsAffected()
	if deleted != 1 {
		return MutationResult[struct{}]{}, ErrConflict
	}
	for _, ref := range refs {
		if err := r.secrets.MarkEndpointSecretOrphaned(ctx, tx, ref, now); err != nil {
			return MutationResult[struct{}]{}, fmt.Errorf("resources: orphan endpoint secret: %w", err)
		}
	}
	if err := r.reconcileRoutingTargetsTx(ctx, tx, userID, models); err != nil {
		return MutationResult[struct{}]{}, err
	}
	out, err := finishEmptyMutation(ctx, tx, decision, http.StatusNoContent)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[struct{}]{}, err
	}
	return out, nil
}

func getEndpointTx(ctx context.Context, tx *sql.Tx, userID, endpointID int64) (Endpoint, error) {
	item, err := scanEndpoint(tx.QueryRowContext(ctx, `
SELECT e.id,e.connector_type,e.base_url,e.note,e.enabled,e.revision,
       (SELECT count(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id),
       e.created_at,e.updated_at,e.mainstream_channel_id,e.mainstream_channel_revision,
       e.mainstream_channel_name,e.mainstream_channel_category
FROM endpoints e WHERE e.id=? AND e.user_id=?`, endpointID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}
	if err != nil {
		return Endpoint{}, fmt.Errorf("resources: read endpoint: %w", err)
	}
	return item, nil
}

func endpointEnableConflictsWithFlattenTx(ctx context.Context, tx *sql.Tx, userID, endpointID int64) (bool, error) {
	var incompatible int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1 FROM model_bindings b
 JOIN models m ON m.id=b.model_id
 JOIN endpoint_keys k ON k.id=b.endpoint_key_id
 JOIN endpoints e ON e.id=k.endpoint_id
 WHERE e.id=? AND e.user_id=? AND m.user_id=?
   AND e.connector_type=? AND k.enabled=1 AND m.flatten_tool_calls=1
)`, endpointID, userID, userID, string(connectorcontract.TypeAnthropicCompatible)).Scan(&incompatible); err != nil {
		return false, fmt.Errorf("resources: validate endpoint flatten restoration: %w", err)
	}
	return incompatible == 1, nil
}

func endpointLockedTx(ctx context.Context, tx *sql.Tx, endpointID int64) (bool, error) {
	var locked int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1 FROM endpoint_keys k JOIN endpoint_key_suspensions s ON s.endpoint_key_id=k.id
 WHERE k.endpoint_id=?
)`, endpointID).Scan(&locked); err != nil {
		return false, fmt.Errorf("resources: read endpoint suspension: %w", err)
	}
	return locked == 1, nil
}

func endpointKeyLockedTx(ctx context.Context, tx *sql.Tx, keyID int64) (bool, error) {
	var locked int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM endpoint_key_suspensions WHERE endpoint_key_id=?)`, keyID).Scan(&locked); err != nil {
		return false, fmt.Errorf("resources: read endpoint key suspension: %w", err)
	}
	return locked == 1, nil
}

func endpointSecretRefsTx(ctx context.Context, tx *sql.Tx, endpointID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT secret_ref_id FROM endpoint_keys WHERE endpoint_id=? ORDER BY id`, endpointID)
	if err != nil {
		return nil, fmt.Errorf("resources: list endpoint secret references: %w", err)
	}
	defer rows.Close()
	var refs []int64
	for rows.Next() {
		var ref int64
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("resources: scan endpoint secret reference: %w", err)
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func verifiedEndpointKeyIDsTx(ctx context.Context, tx *sql.Tx, userID, endpointID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT k.id
FROM endpoint_keys k
JOIN endpoints e ON e.id=k.endpoint_id
WHERE e.user_id=? AND e.id=?
ORDER BY k.id`, userID, endpointID)
	if err != nil {
		return nil, fmt.Errorf("resources: list verified endpoint keys: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("resources: scan verified endpoint key: %w", err)
		}
		if id <= 0 {
			return nil, ErrUnavailable
		}
		if len(ids) == 0 || ids[len(ids)-1] != id {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resources: iterate verified endpoint keys: %w", err)
	}
	return ids, nil
}

type bindingRevisionTarget struct {
	modelID  int64
	revision int64
}

func bindingRevisionTargetsForKeysTx(ctx context.Context, tx *sql.Tx, userID int64, keyIDs []int64) ([]bindingRevisionTarget, error) {
	if len(keyIDs) == 0 {
		return []bindingRevisionTarget{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keyIDs)), ",")
	arguments := make([]any, 0, len(keyIDs)+1)
	arguments = append(arguments, userID)
	for _, keyID := range keyIDs {
		arguments = append(arguments, keyID)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT m.id,m.binding_revision
FROM model_bindings b
JOIN models m ON m.id=b.model_id
WHERE m.user_id=? AND b.endpoint_key_id IN (`+placeholders+`)
ORDER BY m.id`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("resources: list deletion-affected models: %w", err)
	}
	defer rows.Close()
	var targets []bindingRevisionTarget
	for rows.Next() {
		var target bindingRevisionTarget
		if err := rows.Scan(&target.modelID, &target.revision); err != nil {
			return nil, fmt.Errorf("resources: scan deletion-affected model: %w", err)
		}
		if target.modelID <= 0 || target.revision < 0 {
			return nil, ErrUnavailable
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resources: iterate deletion-affected models: %w", err)
	}
	return targets, nil
}

func validateBindingRevisionTargets(targets []bindingRevisionTarget) error {
	const maxRevision = int64(1<<63 - 1)
	for _, target := range targets {
		if target.revision == maxRevision {
			return ErrConflict
		}
	}
	return nil
}

func advanceBindingRevisionsForDeletionTx(ctx context.Context, tx *sql.Tx, userID int64, targets []bindingRevisionTarget, now int64) error {
	for _, target := range targets {
		result, err := tx.ExecContext(ctx, `
UPDATE models SET binding_revision=binding_revision+1,updated_at=?
WHERE id=? AND user_id=? AND binding_revision=?`, now, target.modelID, userID, target.revision)
		if err != nil {
			return fmt.Errorf("resources: advance deletion binding revision: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("resources: observe deletion binding revision: %w", err)
		}
		if updated != 1 {
			return ErrConflict
		}
	}
	return nil
}

type endpointKeyRow struct {
	id, endpointID, revision, createdAt, updatedAt int64
	displayHead, displayTail, note                 string
	enabled, forceStoreFalse, suspended            int
}

func (row endpointKeyRow) dto() (EndpointKey, error) {
	id, err := decimalID(row.id)
	if err != nil {
		return EndpointKey{}, err
	}
	endpointID, err := decimalID(row.endpointID)
	if err != nil || row.revision < 1 || row.enabled < 0 || row.enabled > 1 || row.forceStoreFalse < 0 || row.forceStoreFalse > 1 || row.suspended < 0 || row.suspended > 1 {
		return EndpointKey{}, ErrUnavailable
	}
	state := "none"
	if row.suspended == 1 {
		state = "security_processing"
	}
	return EndpointKey{
		ID: id, EndpointID: endpointID, DisplayHead: row.displayHead, DisplayTail: row.displayTail,
		Note: row.note, Enabled: row.enabled == 1, ForceStoreFalse: row.forceStoreFalse == 1,
		SuspensionState: state, Revision: strconv.FormatInt(row.revision, 10),
		CreatedAt: row.createdAt, UpdatedAt: row.updatedAt,
	}, nil
}

func scanEndpointKey(scanner interface{ Scan(...any) error }) (EndpointKey, error) {
	var row endpointKeyRow
	if err := scanner.Scan(&row.id, &row.endpointID, &row.displayHead, &row.displayTail, &row.note,
		&row.enabled, &row.forceStoreFalse, &row.suspended, &row.revision, &row.createdAt, &row.updatedAt); err != nil {
		return EndpointKey{}, err
	}
	return row.dto()
}

const endpointKeySelect = `
SELECT k.id,k.endpoint_id,k.display_head,k.display_tail,k.note,k.enabled,k.force_store_false,
       EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id),
       k.revision,k.created_at,k.updated_at
FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id`

func (r *Repository) ListEndpointKeys(ctx context.Context, userID, endpointID int64, limit int, cursor string) (Page[EndpointKey], error) {
	limit = normalizePageLimit(limit)
	if r == nil || userID <= 0 || endpointID <= 0 || !validPageLimit(limit) {
		return Page[EndpointKey]{}, ErrInvalidRequest
	}
	if _, err := r.GetEndpoint(ctx, userID, endpointID); err != nil {
		return Page[EndpointKey]{}, err
	}
	now, err := r.nowUnix()
	if err != nil {
		return Page[EndpointKey]{}, err
	}
	var afterUpdated, afterID uint64
	if cursor != "" {
		owner := strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(endpointID, 10)
		atoms, err := r.cursors.decode(cursor, "endpoint-keys", owner, uint64(now), db.CursorUint, db.CursorUint)
		if err != nil {
			return Page[EndpointKey]{}, err
		}
		afterUpdated, afterID = atoms[0].Uint, atoms[1].Uint
		if afterUpdated > uint64(maxUnixSecond) || afterID == 0 || afterID > uint64(^uint64(0)>>1) {
			return Page[EndpointKey]{}, ErrInvalidRequest
		}
	}
	rows, err := r.db.QueryContext(ctx, endpointKeySelect+`
WHERE e.user_id=? AND e.id=?
  AND (?=0 OR k.updated_at<? OR (k.updated_at=? AND k.id<?))
ORDER BY k.updated_at DESC,k.id DESC LIMIT ?`, userID, endpointID, afterID, afterUpdated, afterUpdated, afterID, limit+1)
	if err != nil {
		return Page[EndpointKey]{}, fmt.Errorf("resources: list endpoint keys: %w", err)
	}
	defer rows.Close()
	items := make([]EndpointKey, 0, limit+1)
	for rows.Next() {
		item, err := scanEndpointKey(rows)
		if err != nil {
			return Page[EndpointKey]{}, fmt.Errorf("resources: scan endpoint key: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[EndpointKey]{}, fmt.Errorf("resources: list endpoint keys: %w", err)
	}
	page := Page[EndpointKey]{Data: items}
	if len(items) > limit {
		last := items[limit-1]
		lastID, _ := parseDecimalID(last.ID)
		owner := strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(endpointID, 10)
		next, err := r.cursors.encode("endpoint-keys", owner, uint64(now+cursorLifetime), []db.CursorAtom{
			{Kind: db.CursorUint, Uint: uint64(last.UpdatedAt)}, {Kind: db.CursorUint, Uint: uint64(lastID)},
		})
		if err != nil {
			return Page[EndpointKey]{}, err
		}
		page.Data = items[:limit]
		page.NextCursor = &next
	}
	return page, nil
}

func (r *Repository) GetEndpointKey(ctx context.Context, userID, endpointID, keyID int64) (EndpointKey, error) {
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 {
		return EndpointKey{}, ErrNotFound
	}
	item, err := scanEndpointKey(r.db.QueryRowContext(ctx, endpointKeySelect+` WHERE e.user_id=? AND e.id=? AND k.id=?`, userID, endpointID, keyID))
	if errors.Is(err, sql.ErrNoRows) {
		return EndpointKey{}, ErrNotFound
	}
	if err != nil {
		return EndpointKey{}, fmt.Errorf("resources: get endpoint key: %w", err)
	}
	return item, nil
}

func (r *Repository) CreateEndpointKey(ctx context.Context, userID, endpointID int64, mutation ControlMutation, input CreateEndpointKeyInput) (MutationResult[EndpointKey], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || mutation.Route != routeEndpointKeys || mutation.Method != http.MethodPost || !mutationPathIDs(mutation, endpointID) || mutation.Query != "" ||
		!input.OwnershipConfirmed || !validateEndpointSecretPlaintext(input.Secret) || !validateNote(input.Note) {
		return MutationResult[EndpointKey]{}, ErrInvalidRequest
	}
	plaintext := append([]byte(nil), input.Secret...)
	defer clear(plaintext)
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[EndpointKey](decision)
	}
	endpoint, err := getEndpointTx(ctx, tx, userID, endpointID)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	if input.ForceStoreFalse && endpoint.ConnectorType != string(connectorcontract.TypeOpenAICompatible) {
		return MutationResult[EndpointKey]{}, ErrInvalidRequest
	}
	locked, err := endpointLockedTx(ctx, tx, endpointID)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	if locked {
		return MutationResult[EndpointKey]{}, ErrResourceLocked
	}
	keyLimit, err := readSiteLimitTx(ctx, tx, "default_endpoint_key_limit", 1, 10000)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	keyCount, err := countTx(ctx, tx, `SELECT count(*) FROM endpoint_keys WHERE endpoint_id=?`, endpointID)
	if err != nil {
		return MutationResult[EndpointKey]{}, fmt.Errorf("resources: count endpoint keys: %w", err)
	}
	if keyCount >= keyLimit {
		return MutationResult[EndpointKey]{}, ErrResourceLimit
	}
	stored, err := r.secrets.WriteEndpointSecret(ctx, tx, SecretWriteInput{
		CanonicalBaseURL: endpoint.BaseURL, ConnectorType: endpoint.ConnectorType,
		Plaintext: plaintext, CreatedAt: now,
	})
	if err != nil {
		return MutationResult[EndpointKey]{}, fmt.Errorf("resources: store endpoint secret: %w", err)
	}
	if stored.RefID <= 0 || len(stored.DisplayHead) > 16 || len(stored.DisplayTail) > 16 {
		return MutationResult[EndpointKey]{}, ErrUnavailable
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO endpoint_keys(endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,1,?,?)`, endpointID, stored.RefID, stored.Fingerprint[:], stored.DisplayHead, stored.DisplayTail,
		input.Note, boolInt(input.Enabled), boolInt(input.ForceStoreFalse), now, now)
	if err != nil {
		return MutationResult[EndpointKey]{}, fmt.Errorf("resources: create endpoint key: %w", err)
	}
	keyID, err := result.LastInsertId()
	if err != nil {
		return MutationResult[EndpointKey]{}, fmt.Errorf("resources: create endpoint key identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO model_discovery_evidence(endpoint_key_id,state,revision,safe_class,safe_diag,fetched_count)
VALUES(?,'unknown',1,'none','',0)`, keyID); err != nil {
		return MutationResult[EndpointKey]{}, fmt.Errorf("resources: initialize discovery evidence: %w", err)
	}
	if err := r.keyCreation.ProtectNewEndpointKey(ctx, tx, userID, keyID, now); err != nil {
		return MutationResult[EndpointKey]{}, fmt.Errorf("resources: protect new endpoint key: %w", err)
	}
	if err := r.projection.ReconcileModelDiscovery(ctx, tx, userID, keyID); err != nil {
		return MutationResult[EndpointKey]{}, fmt.Errorf("resources: project new endpoint key discovery: %w", err)
	}
	if input.ForceStoreFalse {
		if err := writePolicyAudit(ctx, tx, userID, "endpoint_key", keyID, "force_store_false", false, true, now); err != nil {
			return MutationResult[EndpointKey]{}, err
		}
	}
	item, err := getEndpointKeyTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusCreated, item)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	return out, nil
}

func (r *Repository) PatchEndpointKey(ctx context.Context, userID, endpointID, keyID int64, mutation ControlMutation, input PatchEndpointKeyInput) (MutationResult[EndpointKey], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 || mutation.Route != routeEndpointKey || mutation.Method != http.MethodPatch || !mutationPathIDs(mutation, endpointID, keyID) || mutation.Query != "" ||
		(input.Note == nil && input.Enabled == nil && input.ForceStoreFalse == nil) || input.ExpectedRevision < 1 || (input.Note != nil && !validateNote(*input.Note)) {
		return MutationResult[EndpointKey]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[EndpointKey](decision)
	}
	current, err := getEndpointKeyTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	endpoint, err := getEndpointTx(ctx, tx, userID, endpointID)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	if input.ForceStoreFalse != nil && *input.ForceStoreFalse && endpoint.ConnectorType != string(connectorcontract.TypeOpenAICompatible) {
		return MutationResult[EndpointKey]{}, ErrInvalidRequest
	}
	currentRevision, _ := strconv.ParseInt(current.Revision, 10, 64)
	if currentRevision != input.ExpectedRevision || currentRevision == int64(^uint64(0)>>1) {
		return MutationResult[EndpointKey]{}, ErrConflict
	}
	if current.SuspensionState != "none" {
		return MutationResult[EndpointKey]{}, ErrResourceLocked
	}
	note, enabled, forceStore := current.Note, current.Enabled, current.ForceStoreFalse
	if input.Note != nil {
		note = *input.Note
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.ForceStoreFalse != nil {
		forceStore = *input.ForceStoreFalse
	}
	if !current.Enabled && enabled {
		incompatible, err := endpointKeyEnableConflictsWithFlattenTx(ctx, tx, userID, endpointID, keyID)
		if err != nil {
			return MutationResult[EndpointKey]{}, err
		}
		if incompatible {
			return MutationResult[EndpointKey]{}, ErrConflict
		}
	}
	var affectedModels []bindingRevisionTarget
	if current.Enabled != enabled {
		affectedModels, err = bindingRevisionTargetsForKeysTx(ctx, tx, userID, []int64{keyID})
		if err != nil {
			return MutationResult[EndpointKey]{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE endpoint_keys SET note=?,enabled=?,force_store_false=?,revision=revision+1,updated_at=?
WHERE id=? AND endpoint_id=? AND revision=?`, note, boolInt(enabled), boolInt(forceStore), now, keyID, endpointID, input.ExpectedRevision)
	if err != nil {
		return MutationResult[EndpointKey]{}, fmt.Errorf("resources: patch endpoint key: %w", err)
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return MutationResult[EndpointKey]{}, ErrConflict
	}
	if current.ForceStoreFalse != forceStore {
		if err := writePolicyAudit(ctx, tx, userID, "endpoint_key", keyID, "force_store_false", current.ForceStoreFalse, forceStore, now); err != nil {
			return MutationResult[EndpointKey]{}, err
		}
	}
	if err := r.reconcileRoutingTargetsTx(ctx, tx, userID, affectedModels); err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	item, err := getEndpointKeyTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, item)
	if err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[EndpointKey]{}, err
	}
	return out, nil
}

func (r *Repository) DeleteEndpointKey(ctx context.Context, userID, endpointID, keyID int64, mutation ControlMutation, expectedRevision int64) (MutationResult[struct{}], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 || expectedRevision < 1 || mutation.Route != routeEndpointKey || mutation.Method != http.MethodDelete || !mutationPathIDs(mutation, endpointID, keyID) || mutation.Query != "" {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[struct{}](decision)
	}
	current, err := getEndpointKeyTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	revision, _ := strconv.ParseInt(current.Revision, 10, 64)
	if revision != expectedRevision {
		return MutationResult[struct{}]{}, ErrConflict
	}
	if current.SuspensionState != "none" {
		return MutationResult[struct{}]{}, ErrResourceLocked
	}
	var secretRef int64
	if err := tx.QueryRowContext(ctx, `SELECT secret_ref_id FROM endpoint_keys WHERE id=? AND endpoint_id=?`, keyID, endpointID).Scan(&secretRef); err != nil {
		return MutationResult[struct{}]{}, ErrNotFound
	}
	models, err := bindingRevisionTargetsForKeysTx(ctx, tx, userID, []int64{keyID})
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := validateBindingRevisionTargets(models); err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := r.projection.PrepareEndpointKeyDeletion(ctx, tx, userID, []int64{keyID}, now); err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: prepare endpoint key projection deletion: %w", err)
	}
	if err := r.keyDeletion.PrepareEndpointKeyDeletion(ctx, tx, userID, []int64{keyID}, now); err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: prepare endpoint key deletion: %w", err)
	}
	if err := advanceBindingRevisionsForDeletionTx(ctx, tx, userID, models, now); err != nil {
		return MutationResult[struct{}]{}, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM endpoint_keys WHERE id=? AND endpoint_id=? AND revision=?`, keyID, endpointID, expectedRevision)
	if err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: delete endpoint key: %w", err)
	}
	deleted, _ := result.RowsAffected()
	if deleted != 1 {
		return MutationResult[struct{}]{}, ErrConflict
	}
	if err := r.secrets.MarkEndpointSecretOrphaned(ctx, tx, secretRef, now); err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: orphan endpoint secret: %w", err)
	}
	if err := r.reconcileRoutingTargetsTx(ctx, tx, userID, models); err != nil {
		return MutationResult[struct{}]{}, err
	}
	out, err := finishEmptyMutation(ctx, tx, decision, http.StatusNoContent)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[struct{}]{}, err
	}
	return out, nil
}

func getEndpointKeyTx(ctx context.Context, tx *sql.Tx, userID, endpointID, keyID int64) (EndpointKey, error) {
	item, err := scanEndpointKey(tx.QueryRowContext(ctx, endpointKeySelect+` WHERE e.user_id=? AND e.id=? AND k.id=?`, userID, endpointID, keyID))
	if errors.Is(err, sql.ErrNoRows) {
		return EndpointKey{}, ErrNotFound
	}
	if err != nil {
		return EndpointKey{}, fmt.Errorf("resources: read endpoint key: %w", err)
	}
	return item, nil
}

func endpointKeyEnableConflictsWithFlattenTx(ctx context.Context, tx *sql.Tx, userID, endpointID, keyID int64) (bool, error) {
	var incompatible int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1 FROM model_bindings b
 JOIN models m ON m.id=b.model_id
 JOIN endpoint_keys k ON k.id=b.endpoint_key_id
 JOIN endpoints e ON e.id=k.endpoint_id
 WHERE e.id=? AND e.user_id=? AND m.user_id=? AND k.id=?
   AND e.connector_type=? AND e.enabled=1 AND m.flatten_tool_calls=1
)`, endpointID, userID, userID, keyID, string(connectorcontract.TypeAnthropicCompatible)).Scan(&incompatible); err != nil {
		return false, fmt.Errorf("resources: validate endpoint key flatten restoration: %w", err)
	}
	return incompatible == 1, nil
}

func writePolicyAudit(ctx context.Context, tx *sql.Tx, actorUserID int64, resourceType string, resourceID int64, policy string, oldValue, newValue bool, at int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO policy_audits(actor_user_id,actor_role,resource_type,resource_id,policy,old_value,new_value,created_at)
VALUES(?,'owner',?,?,?,?,?,?)`, actorUserID, resourceType, resourceID, policy, boolInt(oldValue), boolInt(newValue), at)
	if err != nil {
		return fmt.Errorf("resources: write policy audit: %w", err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
