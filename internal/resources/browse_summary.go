package resources

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Browse projections are attached only to numbered collections. Existing
// cursor and mutation responses retain their original resource shape.
type EndpointBrowse struct {
	ModelCount        string `json:"model_count"`
	AvailableKeyCount string `json:"available_key_count"`
	State             string `json:"state"`
}

type EndpointKeyBrowse struct {
	DonationEligibility   string            `json:"donation_eligibility"`
	ModelCount            string            `json:"model_count"`
	BindingCount          string            `json:"binding_count"`
	AvailableBindingCount string            `json:"available_binding_count"`
	Discovery             DiscoveryEvidence `json:"discovery"`
	Preview               []KeyBindingView  `json:"preview"`
}

type ModelBrowse struct {
	AvailableBindingCount string           `json:"available_binding_count"`
	Preview               []KeyBindingView `json:"preview"`
}

// KeyBindingView contains only the current owner's resource labels. Its state
// describes configured routing eligibility, not a promise that a call succeeds.
type KeyBindingView struct {
	ID              string `json:"id"`
	ModelID         string `json:"model_id"`
	ModelFullName   string `json:"model_full_name"`
	EndpointID      string `json:"endpoint_id"`
	EndpointKeyID   string `json:"endpoint_key_id"`
	EndpointBaseURL string `json:"endpoint_base_url"`
	ConnectorType   string `json:"connector_type"`
	EndpointNote    string `json:"endpoint_note"`
	DisplayHead     string `json:"display_head"`
	DisplayTail     string `json:"display_tail"`
	KeyNote         string `json:"key_note"`
	UpstreamModelID string `json:"upstream_model_id"`
	Ord             int    `json:"ord"`
	MaxConcurrency  int64  `json:"max_concurrency"`
	MaxRPM          int64  `json:"max_rpm"`
	State           string `json:"state"`
}

const ownedBindingJoin = ` FROM model_bindings b
JOIN models m ON m.id=b.model_id
JOIN endpoint_keys k ON k.id=b.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
LEFT JOIN endpoint_key_limits kl ON kl.endpoint_key_id=k.id`

// Keep eligibility aligned with bindingSelectionConnectorTx: current manual
// support or current successful discovery, and no physical disable/suspension.
const ownedBindingState = `CASE
WHEN e.enabled=0 THEN 'endpoint_disabled'
WHEN k.enabled=0 THEN 'key_disabled'
WHEN EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id) THEN 'key_suspended'
WHEN NOT EXISTS(
 SELECT 1 FROM model_pair_catalog p JOIN model_discovery_evidence d ON d.endpoint_key_id=p.endpoint_key_id
 WHERE p.endpoint_key_id=k.id AND p.normalized_model_id=b.upstream_model_id
 AND (p.manual_supports>0 OR (p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision))
) THEN 'unsupported'
ELSE 'available' END`

const ownedBindingSelect = `SELECT b.id,m.id,m.full_name,e.id,k.id,e.base_url,e.connector_type,e.note,
k.display_head,k.display_tail,k.note,b.upstream_model_id,b.ord,COALESCE(kl.max_concurrency,0),COALESCE(kl.max_rpm,0),` + ownedBindingState + ownedBindingJoin

const ownedBindingScope = ` WHERE m.user_id=? AND e.user_id=?`

func scanKeyBinding(s pageScanner) (KeyBindingView, error) {
	var value KeyBindingView
	var bindingID, modelID, endpointID, keyID int64
	err := s.Scan(&bindingID, &modelID, &value.ModelFullName, &endpointID, &keyID,
		&value.EndpointBaseURL, &value.ConnectorType, &value.EndpointNote, &value.DisplayHead, &value.DisplayTail,
		&value.KeyNote, &value.UpstreamModelID, &value.Ord, &value.MaxConcurrency, &value.MaxRPM, &value.State)
	if err != nil {
		return value, err
	}
	if bindingID <= 0 || modelID <= 0 || endpointID <= 0 || keyID <= 0 || value.Ord < 0 || value.MaxConcurrency < 0 || value.MaxRPM < 0 {
		return value, ErrUnavailable
	}
	value.ID, value.ModelID = strconv.FormatInt(bindingID, 10), strconv.FormatInt(modelID, 10)
	value.EndpointID, value.EndpointKeyID = strconv.FormatInt(endpointID, 10), strconv.FormatInt(keyID, 10)
	return value, nil
}

func bindingPreviewTx(ctx context.Context, tx *sql.Tx, scope, order string, args []any) ([]KeyBindingView, error) {
	rows, err := tx.QueryContext(ctx, ownedBindingSelect+scope+` ORDER BY `+order+` LIMIT 3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]KeyBindingView, 0, 3)
	for rows.Next() {
		item, err := scanKeyBinding(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func endpointBrowseTx(ctx context.Context, tx *sql.Tx, userID int64, endpoint Endpoint) (*EndpointBrowse, error) {
	var modelCount, availableKeys int64
	err := tx.QueryRowContext(ctx, `SELECT
(SELECT COUNT(DISTINCT b.model_id)`+ownedBindingJoin+ownedBindingScope+` AND e.id=?),
(SELECT COUNT(*) FROM endpoint_keys k WHERE k.endpoint_id=? AND k.enabled=1
 AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id))`,
		userID, userID, endpoint.ID, endpoint.ID).Scan(&modelCount, &availableKeys)
	if err != nil {
		return nil, err
	}
	state := "available"
	if !endpoint.Enabled {
		state = "endpoint_disabled"
		availableKeys = 0
	} else if endpoint.KeyCount == "0" {
		state = "no_keys"
	} else if availableKeys == 0 {
		state = "no_usable_key"
	}
	return &EndpointBrowse{ModelCount: strconv.FormatInt(modelCount, 10), AvailableKeyCount: strconv.FormatInt(availableKeys, 10), State: state}, nil
}

func endpointKeyBrowseTx(ctx context.Context, tx *sql.Tx, userID, endpointID int64, key EndpointKey) (*EndpointKeyBrowse, error) {
	keyID, err := parseDecimalID(key.ID)
	if err != nil {
		return nil, err
	}
	evidenceRow, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return nil, err
	}
	evidence, err := evidenceRow.evidence()
	if err != nil {
		return nil, err
	}
	scope := ownedBindingScope + ` AND e.id=? AND k.id=?`
	args := []any{userID, userID, endpointID, keyID}
	var modelCount, bindingCount, availableCount int64
	err = tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT m.id),COUNT(*),COALESCE(SUM(CASE WHEN `+ownedBindingState+`='available' THEN 1 ELSE 0 END),0)`+ownedBindingJoin+scope, args...).Scan(&modelCount, &bindingCount, &availableCount)
	if err != nil {
		return nil, err
	}
	preview, err := bindingPreviewTx(ctx, tx, scope, "b.upstream_model_id,b.id", args)
	if err != nil {
		return nil, err
	}
	// Submission uses the same persisted suspension and membership checks.
	// A due membership remains occupied until the ordinary expiry path ends it.
	var eligibility string
	err = tx.QueryRowContext(ctx, `SELECT CASE
WHEN EXISTS(SELECT 1 FROM endpoint_key_suspensions WHERE endpoint_key_id=?) THEN 'security_processing'
WHEN EXISTS(SELECT 1 FROM donation_key_memberships WHERE endpoint_key_id=?) THEN 'already_donated'
ELSE 'eligible' END`, keyID, keyID).Scan(&eligibility)
	if err != nil {
		return nil, err
	}
	return &EndpointKeyBrowse{
		DonationEligibility: eligibility,
		ModelCount:          strconv.FormatInt(modelCount, 10), BindingCount: strconv.FormatInt(bindingCount, 10),
		AvailableBindingCount: strconv.FormatInt(availableCount, 10), Discovery: evidence, Preview: preview,
	}, nil
}

func modelBrowseTx(ctx context.Context, tx *sql.Tx, userID int64, model Model) (*ModelBrowse, error) {
	scope := ownedBindingScope + ` AND m.id=?`
	args := []any{userID, userID, model.ID}
	var availableCount int64
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+ownedBindingJoin+scope+` AND `+ownedBindingState+`='available'`, args...).Scan(&availableCount)
	if err != nil {
		return nil, err
	}
	preview, err := bindingPreviewTx(ctx, tx, scope, "b.ord,b.id", args)
	if err != nil {
		return nil, err
	}
	return &ModelBrowse{AvailableBindingCount: strconv.FormatInt(availableCount, 10), Preview: preview}, nil
}

func (r *Repository) ListKeyBindingsPage(ctx context.Context, userID, endpointID, keyID int64, upstreamModel string, page pagination.Request) (Page[KeyBindingView], error) {
	if endpointID <= 0 || keyID <= 0 || upstreamModel != "" && !validateCatalogText(upstreamModel, 1, 512) {
		return Page[KeyBindingView]{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, page, func(ctx context.Context, tx *sql.Tx) (Page[KeyBindingView], error) {
		if _, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID); err != nil {
			return Page[KeyBindingView]{}, err
		}
		scope := ownedBindingScope + ` AND e.id=? AND k.id=?`
		args := []any{userID, userID, endpointID, keyID}
		if upstreamModel != "" {
			scope += ` AND b.upstream_model_id=?`
			args = append(args, upstreamModel)
		}
		return readNumberedPage(ctx, tx, page, ownedBindingSelect+scope, "b.upstream_model_id,b.id", args, func(s pageScanner) (KeyBindingView, error) { return scanKeyBinding(s) })
	})
}
