package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const (
	routeMainstreamChannels    = "/admin/api/mainstream-channels"
	routeMainstreamChannel     = "/admin/api/mainstream-channels/{id}"
	routeEndpointCreateOptions = "/api/endpoint-create-options"

	mainstreamChannelStateActive          = "active"
	mainstreamChannelStateRetired         = "retired"
	mainstreamChannelStateAll             = "all"
	mainstreamChannelCategorySubscription = "subscription"
	mainstreamChannelCategoryAPIPlatform  = "api_platform"
)

func validMainstreamChannelID(value string) bool {
	return db.ValidateOpaqueID(value, "mch_")
}

func validMainstreamChannelState(value string) bool {
	return value == mainstreamChannelStateActive || value == mainstreamChannelStateRetired || value == mainstreamChannelStateAll
}

func validMainstreamChannelCategory(value string) bool {
	return value == mainstreamChannelCategorySubscription || value == mainstreamChannelCategoryAPIPlatform
}

func validMainstreamConnectorType(registry interface {
	MustValidate(connectorcontract.Type) (connectorcontract.Type, error)
}, value string) bool {
	validated, err := registry.MustValidate(connectorcontract.Type(value))
	if err != nil || string(validated) != value {
		return false
	}
	// Generation 2's authority table is intentionally closed to the two
	// connectors currently supported by the endpoint schema.
	return validated == connectorcontract.TypeOpenAICompatible || validated == connectorcontract.TypeAnthropicCompatible
}

type mainstreamChannelRow struct {
	id            string
	name          string
	category      string
	connectorType string
	baseURL       string
	enabled       int64
	state         string
	revision      int64
	createdAt     int64
	updatedAt     int64
	retiredAt     sql.NullInt64
}

func (row mainstreamChannelRow) dto() (MainstreamChannel, error) {
	if !validMainstreamChannelID(row.id) || !validateExactText(row.name, 1, 128) ||
		!validMainstreamChannelCategory(row.category) || row.enabled < 0 || row.enabled > 1 ||
		(row.state != mainstreamChannelStateActive && row.state != mainstreamChannelStateRetired) || row.revision < 1 ||
		row.createdAt < 0 || row.createdAt > maxUnixSecond || row.updatedAt < 0 || row.updatedAt > maxUnixSecond {
		return MainstreamChannel{}, ErrUnavailable
	}
	if row.state == mainstreamChannelStateActive && (row.enabled != 0 && row.enabled != 1 || row.retiredAt.Valid) {
		return MainstreamChannel{}, ErrUnavailable
	}
	if row.state == mainstreamChannelStateRetired && (!row.retiredAt.Valid || row.enabled != 0 || row.retiredAt.Int64 < 0 || row.retiredAt.Int64 > maxUnixSecond) {
		return MainstreamChannel{}, ErrUnavailable
	}
	if row.connectorType != string(connectorcontract.TypeOpenAICompatible) && row.connectorType != string(connectorcontract.TypeAnthropicCompatible) {
		return MainstreamChannel{}, ErrUnavailable
	}
	var retiredAt *int64
	if row.retiredAt.Valid {
		value := row.retiredAt.Int64
		retiredAt = &value
	}
	return MainstreamChannel{
		ID: row.id, Name: row.name, Category: row.category, ConnectorType: row.connectorType,
		BaseURL: row.baseURL, Enabled: row.enabled == 1, State: row.state,
		Revision: strconv.FormatInt(row.revision, 10), CreatedAt: row.createdAt,
		UpdatedAt: row.updatedAt, RetiredAt: retiredAt,
	}, nil
}

func scanMainstreamChannel(scanner interface{ Scan(...any) error }) (MainstreamChannel, error) {
	var row mainstreamChannelRow
	if err := scanner.Scan(&row.id, &row.name, &row.category, &row.connectorType, &row.baseURL,
		&row.enabled, &row.state, &row.revision, &row.createdAt, &row.updatedAt, &row.retiredAt); err != nil {
		return MainstreamChannel{}, err
	}
	return row.dto()
}

const mainstreamChannelSelect = `
SELECT id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at,retired_at
FROM mainstream_channels`

func getMainstreamChannelTx(ctx context.Context, tx *sql.Tx, id string) (MainstreamChannel, error) {
	item, err := scanMainstreamChannel(tx.QueryRowContext(ctx, mainstreamChannelSelect+` WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return MainstreamChannel{}, ErrNotFound
	}
	if err != nil {
		return MainstreamChannel{}, fmt.Errorf("resources: read mainstream channel: %w", err)
	}
	return item, nil
}

func getMainstreamChannelSnapshotTx(ctx context.Context, tx *sql.Tx, id string) (mainstreamChannelRow, error) {
	var row mainstreamChannelRow
	err := tx.QueryRowContext(ctx, mainstreamChannelSelect+` WHERE id=?`, id).Scan(
		&row.id, &row.name, &row.category, &row.connectorType, &row.baseURL,
		&row.enabled, &row.state, &row.revision, &row.createdAt, &row.updatedAt, &row.retiredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return mainstreamChannelRow{}, ErrNotFound
	}
	if err != nil {
		return mainstreamChannelRow{}, fmt.Errorf("resources: read mainstream channel: %w", err)
	}
	return row, nil
}

func (r *Repository) validateMainstreamChannelDTO(item MainstreamChannel) error {
	if r == nil || isNilInterface(r.baseURLs) || !validMainstreamConnectorType(r.connectors, item.ConnectorType) {
		return ErrUnavailable
	}
	canonical, err := r.baseURLs.ValidateBaseURL(item.BaseURL)
	if err != nil || canonical != item.BaseURL {
		return ErrUnavailable
	}
	return nil
}

func validateMainstreamChannelCreate(r *Repository, input CreateMainstreamChannelInput) (string, error) {
	if r == nil || !validateExactText(input.Name, 1, 128) || !validMainstreamChannelCategory(input.Category) ||
		!validMainstreamConnectorType(r.connectors, input.ConnectorType) || isNilInterface(r.baseURLs) {
		return "", ErrInvalidRequest
	}
	canonical, err := r.baseURLs.ValidateBaseURL(input.BaseURL)
	if err != nil || canonical == "" || len(canonical) > 4096 {
		return "", ErrInvalidRequest
	}
	return canonical, nil
}

func validateMainstreamChannelPatch(r *Repository, input PatchMainstreamChannelInput) (string, error) {
	if r == nil || input.ExpectedRevision < 1 ||
		(input.Name == nil && input.Category == nil && input.ConnectorType == nil && input.BaseURL == nil && input.Enabled == nil) {
		return "", ErrInvalidRequest
	}
	if input.Name != nil && !validateExactText(*input.Name, 1, 128) {
		return "", ErrInvalidRequest
	}
	if input.Category != nil && !validMainstreamChannelCategory(*input.Category) {
		return "", ErrInvalidRequest
	}
	if input.ConnectorType != nil && !validMainstreamConnectorType(r.connectors, *input.ConnectorType) {
		return "", ErrInvalidRequest
	}
	if input.BaseURL == nil {
		return "", nil
	}
	if isNilInterface(r.baseURLs) {
		return "", ErrInvalidRequest
	}
	canonical, err := r.baseURLs.ValidateBaseURL(*input.BaseURL)
	if err != nil || canonical == "" || len(canonical) > 4096 {
		return "", ErrInvalidRequest
	}
	return canonical, nil
}

func mapMainstreamChannelWriteError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.Contains(message, "active mainstream channel limit exceeded") {
		return ErrResourceLimit
	}
	if strings.Contains(message, "UNIQUE constraint failed: mainstream_channels.id") {
		return ErrConflict
	}
	return err
}

func mainstreamChannelMutationPathIDs(mutation ControlMutation, ids ...string) bool {
	if len(mutation.PathIDs) != len(ids) {
		return false
	}
	for index, id := range ids {
		if !validMainstreamChannelID(id) || mutation.PathIDs[index] != id {
			return false
		}
	}
	return true
}

func adminMutationForTextPath(writer http.ResponseWriter, request *http.Request, route string, pathIDs []string, canonical any) (ControlMutation, bool) {
	key, ok := idempotencyKey(writer, request)
	if !ok {
		return ControlMutation{}, false
	}
	body, err := idempotency.CanonicalJSON(canonical)
	if err != nil {
		writeResourceError(writer, ErrInvalidRequest)
		return ControlMutation{}, false
	}
	for _, id := range pathIDs {
		if !validMainstreamChannelID(id) {
			writeResourceError(writer, ErrInvalidRequest)
			return ControlMutation{}, false
		}
	}
	return ControlMutation{IdempotencyKey: key, Method: request.Method, Route: route,
		PathIDs: append([]string(nil), pathIDs...), CanonicalBody: body}, true
}

func (r *Repository) ListMainstreamChannels(ctx context.Context, adminID int64, state string, limit int, cursor string) (Page[MainstreamChannel], error) {
	if state == "" {
		state = mainstreamChannelStateActive
	}
	limit = normalizePageLimit(limit)
	if r == nil || adminID <= 0 || !validMainstreamChannelState(state) || !validPageLimit(limit) {
		return Page[MainstreamChannel]{}, ErrInvalidRequest
	}
	tx, now, err := r.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return Page[MainstreamChannel]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	afterUpdated := uint64(0)
	afterID := ""
	if cursor != "" {
		owner := strconv.FormatInt(adminID, 10) + "|" + state
		atoms, err := r.cursors.decode(cursor, "admin-mainstream-channels", owner, uint64(now), db.CursorUint, db.CursorText)
		if err != nil {
			return Page[MainstreamChannel]{}, err
		}
		afterUpdated, afterID = atoms[0].Uint, atoms[1].Text
		if afterUpdated > uint64(maxUnixSecond) || !validMainstreamChannelID(afterID) {
			return Page[MainstreamChannel]{}, ErrInvalidRequest
		}
	}
	rows, err := tx.QueryContext(ctx, mainstreamChannelSelect+`
WHERE (?='all' OR state=?)
  AND (?='' OR updated_at<? OR (updated_at=? AND id<?))
ORDER BY updated_at DESC,id DESC
LIMIT ?`, state, state, afterID, afterUpdated, afterUpdated, afterID, limit+1)
	if err != nil {
		return Page[MainstreamChannel]{}, fmt.Errorf("resources: list mainstream channels: %w", err)
	}
	defer rows.Close()
	items := make([]MainstreamChannel, 0, limit+1)
	for rows.Next() {
		item, err := scanMainstreamChannel(rows)
		if err != nil {
			return Page[MainstreamChannel]{}, fmt.Errorf("resources: scan mainstream channel: %w", err)
		}
		if err := r.validateMainstreamChannelDTO(item); err != nil {
			return Page[MainstreamChannel]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[MainstreamChannel]{}, fmt.Errorf("resources: list mainstream channels: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Page[MainstreamChannel]{}, fmt.Errorf("resources: close mainstream channels: %w", err)
	}
	page := Page[MainstreamChannel]{Data: items}
	if len(items) > limit {
		last := items[limit-1]
		updated, err := strconv.ParseUint(strconv.FormatInt(last.UpdatedAt, 10), 10, 64)
		if err != nil {
			return Page[MainstreamChannel]{}, ErrUnavailable
		}
		owner := strconv.FormatInt(adminID, 10) + "|" + state
		next, err := r.cursors.encode("admin-mainstream-channels", owner, uint64(now+cursorLifetime), []db.CursorAtom{
			{Kind: db.CursorUint, Uint: updated}, {Kind: db.CursorText, Text: last.ID},
		})
		if err != nil {
			return Page[MainstreamChannel]{}, err
		}
		page.Data = items[:limit]
		page.NextCursor = &next
	}
	if err := commitTx(tx, &committed); err != nil {
		return Page[MainstreamChannel]{}, err
	}
	return page, nil
}

func (r *Repository) GetMainstreamChannel(ctx context.Context, adminID int64, id string) (MainstreamChannel, error) {
	if r == nil || adminID <= 0 || !validMainstreamChannelID(id) {
		return MainstreamChannel{}, ErrNotFound
	}
	tx, _, err := r.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return MainstreamChannel{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	item, err := getMainstreamChannelTx(ctx, tx, id)
	if err != nil {
		return MainstreamChannel{}, err
	}
	if err := r.validateMainstreamChannelDTO(item); err != nil {
		return MainstreamChannel{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MainstreamChannel{}, err
	}
	return item, nil
}

func (r *Repository) CreateMainstreamChannel(ctx context.Context, adminID int64, mutation ControlMutation, input CreateMainstreamChannelInput) (MutationResult[MainstreamChannel], error) {
	if r == nil || adminID <= 0 || mutation.Route != routeMainstreamChannels || mutation.Method != http.MethodPost || len(mutation.PathIDs) != 0 || mutation.Query != "" {
		return MutationResult[MainstreamChannel]{}, ErrInvalidRequest
	}
	canonicalURL, err := validateMainstreamChannelCreate(r, input)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	tx, now, err := r.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginAdminControlMutation(ctx, tx, adminID, mutation, now)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[MainstreamChannel](decision)
	}
	channelID, err := r.mainstreamChannelID()
	if err != nil || !validMainstreamChannelID(channelID) {
		return MutationResult[MainstreamChannel]{}, ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at,retired_at)
VALUES(?,?,?,?,?,?,?,1,?,?,NULL)`, channelID, input.Name, input.Category, input.ConnectorType, canonicalURL, boolInt(input.Enabled), mainstreamChannelStateActive, now, now)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, mapMainstreamChannelWriteError(fmt.Errorf("resources: create mainstream channel: %w", err))
	}
	item, err := getMainstreamChannelTx(ctx, tx, channelID)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	if err := r.validateMainstreamChannelDTO(item); err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusCreated, item)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	return out, nil
}

func (r *Repository) PatchMainstreamChannel(ctx context.Context, adminID int64, id string, mutation ControlMutation, input PatchMainstreamChannelInput) (MutationResult[MainstreamChannel], error) {
	if r == nil || adminID <= 0 || !validMainstreamChannelID(id) || mutation.Route != routeMainstreamChannel || mutation.Method != http.MethodPatch || !mainstreamChannelMutationPathIDs(mutation, id) || mutation.Query != "" {
		return MutationResult[MainstreamChannel]{}, ErrInvalidRequest
	}
	canonicalURL, err := validateMainstreamChannelPatch(r, input)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	tx, now, err := r.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginAdminControlMutation(ctx, tx, adminID, mutation, now)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[MainstreamChannel](decision)
	}
	current, err := getMainstreamChannelTx(ctx, tx, id)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	if err := r.validateMainstreamChannelDTO(current); err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	currentRevision, err := strconv.ParseInt(current.Revision, 10, 64)
	if err != nil || currentRevision != input.ExpectedRevision {
		return MutationResult[MainstreamChannel]{}, ErrConflict
	}
	if current.State != mainstreamChannelStateActive {
		return MutationResult[MainstreamChannel]{}, ErrConflict
	}
	name, category, connectorType, baseURL := current.Name, current.Category, current.ConnectorType, current.BaseURL
	enabled := current.Enabled
	if input.Name != nil {
		name = *input.Name
	}
	if input.Category != nil {
		category = *input.Category
	}
	if input.ConnectorType != nil {
		connectorType = *input.ConnectorType
	}
	if input.BaseURL != nil {
		baseURL = canonicalURL
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	result, err := tx.ExecContext(ctx, `
UPDATE mainstream_channels
SET name=?,category=?,connector_type=?,canonical_base_url=?,enabled=?,revision=revision+1,updated_at=?
WHERE id=? AND state='active' AND revision=?`, name, category, connectorType, baseURL, boolInt(enabled), now, id, input.ExpectedRevision)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, mapMainstreamChannelWriteError(fmt.Errorf("resources: patch mainstream channel: %w", err))
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return MutationResult[MainstreamChannel]{}, ErrConflict
	}
	item, err := getMainstreamChannelTx(ctx, tx, id)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	if err := r.validateMainstreamChannelDTO(item); err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, item)
	if err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[MainstreamChannel]{}, err
	}
	return out, nil
}

func (r *Repository) RetireMainstreamChannel(ctx context.Context, adminID int64, id string, mutation ControlMutation, expectedRevision int64) (MutationResult[struct{}], error) {
	if r == nil || adminID <= 0 || !validMainstreamChannelID(id) || expectedRevision < 1 || mutation.Route != routeMainstreamChannel || mutation.Method != http.MethodDelete || !mainstreamChannelMutationPathIDs(mutation, id) || mutation.Query != "" {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	tx, now, err := r.beginAuthorizedAdminTx(ctx, adminID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginAdminControlMutation(ctx, tx, adminID, mutation, now)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[struct{}](decision)
	}
	current, err := getMainstreamChannelTx(ctx, tx, id)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := r.validateMainstreamChannelDTO(current); err != nil {
		return MutationResult[struct{}]{}, err
	}
	currentRevision, err := strconv.ParseInt(current.Revision, 10, 64)
	if err != nil || currentRevision != expectedRevision || current.State != mainstreamChannelStateActive {
		return MutationResult[struct{}]{}, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `
UPDATE mainstream_channels
SET state='retired',enabled=0,revision=revision+1,updated_at=?,retired_at=?
WHERE id=? AND state='active' AND revision=?`, now, now, id, expectedRevision)
	if err != nil {
		return MutationResult[struct{}]{}, mapMainstreamChannelWriteError(fmt.Errorf("resources: retire mainstream channel: %w", err))
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return MutationResult[struct{}]{}, ErrConflict
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

func (r *Repository) GetEndpointCreateOptions(ctx context.Context, userID int64) (EndpointCreateOptions, error) {
	if r == nil || userID <= 0 {
		return EndpointCreateOptions{}, ErrInvalidRequest
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return EndpointCreateOptions{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	types := r.connectors.Types()
	baseTypes := make([]string, len(types))
	for index, connectorType := range types {
		baseTypes[index] = string(connectorType)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT id,name,connector_type,canonical_base_url
FROM mainstream_channels
WHERE state='active' AND enabled=1
ORDER BY name COLLATE BINARY ASC,id ASC`)
	if err != nil {
		return EndpointCreateOptions{}, fmt.Errorf("resources: list endpoint create options: %w", err)
	}
	defer rows.Close()
	channels := make([]MainstreamChannelOption, 0)
	for rows.Next() {
		var option MainstreamChannelOption
		if err := rows.Scan(&option.ID, &option.Name, &option.ConnectorType, &option.BaseURL); err != nil {
			return EndpointCreateOptions{}, fmt.Errorf("resources: scan endpoint create option: %w", err)
		}
		if !validMainstreamChannelID(option.ID) || !validateExactText(option.Name, 1, 128) || !validMainstreamConnectorType(r.connectors, option.ConnectorType) || option.BaseURL == "" || len(option.BaseURL) > 4096 {
			return EndpointCreateOptions{}, ErrUnavailable
		}
		canonical, err := r.baseURLs.ValidateBaseURL(option.BaseURL)
		if err != nil || canonical != option.BaseURL {
			return EndpointCreateOptions{}, ErrUnavailable
		}
		channels = append(channels, option)
	}
	if err := rows.Err(); err != nil {
		return EndpointCreateOptions{}, fmt.Errorf("resources: list endpoint create options: %w", err)
	}
	if err := rows.Close(); err != nil {
		return EndpointCreateOptions{}, fmt.Errorf("resources: close endpoint create options: %w", err)
	}
	if err := commitTx(tx, &committed); err != nil {
		return EndpointCreateOptions{}, err
	}
	return EndpointCreateOptions{BaseConnectorTypes: baseTypes, MainstreamChannels: channels}, nil
}
