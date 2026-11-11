package resources

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Browse projections accompany numbered collections and endpoint details. Existing
// cursor and mutation responses retain their original resource shape.
type EndpointBrowse struct {
	ModelCount           string   `json:"model_count"`
	AvailableKeyCount    string   `json:"available_key_count"`
	State                string   `json:"state"`
	MainstreamCategories []string `json:"mainstream_categories"`
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
	TargetID              string `json:"target_id"`
	ID                    string `json:"id"`
	ModelID               string `json:"model_id"`
	ModelFullName         string `json:"model_full_name"`
	EndpointID            string `json:"endpoint_id"`
	EndpointKeyID         string `json:"endpoint_key_id"`
	EndpointBaseURL       string `json:"endpoint_base_url"`
	ConnectorType         string `json:"connector_type"`
	EndpointNote          string `json:"endpoint_note"`
	DisplayHead           string `json:"display_head"`
	DisplayTail           string `json:"display_tail"`
	KeyNote               string `json:"key_note"`
	UpstreamModelID       string `json:"upstream_model_id"`
	Ord                   int    `json:"ord"`
	MaxConcurrency        int64  `json:"max_concurrency"`
	MaxRPM                int64  `json:"max_rpm"`
	ConcurrencyOverflow   string `json:"concurrency_overflow"`
	RPMOverflow           string `json:"rpm_overflow"`
	ConcurrencyQueueLimit int64  `json:"concurrency_queue_limit"`
	RPMQueueLimit         int64  `json:"rpm_queue_limit"`
	State                 string `json:"state"`
}

const ownedBindingJoin = ` FROM model_bindings b JOIN model_binding_targets t ON t.binding_id=b.id
JOIN models m ON m.id=b.model_id
JOIN endpoint_key_current_credentials k ON k.id=t.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
LEFT JOIN endpoint_key_limits kl ON kl.endpoint_key_id=k.id`

// Explicit targets survive discovery refreshes; current grants decide whether
// their selected services remain configured for use.
const ownedBindingState = `CASE
WHEN e.enabled=0 THEN 'endpoint_disabled'
WHEN k.enabled=0 THEN 'key_disabled'
WHEN EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id) THEN 'key_suspended'
WHEN NOT EXISTS(SELECT 1 FROM self_grant_availability a WHERE a.endpoint_key_id=k.id AND (a.native_service_mask&t.native_service_mask)>0) THEN 'unsupported'
ELSE 'available' END`

const ownedBindingSelect = `SELECT b.id,m.id,m.full_name,e.id,k.id,e.base_url,e.connector_type,e.note,
k.display_head,k.display_tail,k.note,t.upstream_model_id,b.ord,COALESCE(kl.max_concurrency,0),COALESCE(kl.max_rpm,0),COALESCE(kl.concurrency_overflow,'reject'),COALESCE(kl.rpm_overflow,'reject'),COALESCE(kl.concurrency_queue_limit,0),COALESCE(kl.rpm_queue_limit,0),` + ownedBindingState + `,t.id` + ownedBindingJoin

const ownedBindingIDSelect = `SELECT t.id` + ownedBindingJoin

const ownedBindingScope = ` WHERE m.user_id=? AND e.user_id=?`

func scanKeyBinding(s pageScanner) (KeyBindingView, error) {
	var value KeyBindingView
	var bindingID, modelID, endpointID, keyID int64
	err := s.Scan(&bindingID, &modelID, &value.ModelFullName, &endpointID, &keyID,
		&value.EndpointBaseURL, &value.ConnectorType, &value.EndpointNote, &value.DisplayHead, &value.DisplayTail,
		&value.KeyNote, &value.UpstreamModelID, &value.Ord, &value.MaxConcurrency, &value.MaxRPM, &value.ConcurrencyOverflow, &value.RPMOverflow, &value.ConcurrencyQueueLimit, &value.RPMQueueLimit, &value.State, &value.TargetID)
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
	// Select the bounded preview before reading labels and live eligibility.
	// Partial ordering must not evaluate that projection for skipped targets.
	rows, err := tx.QueryContext(ctx, `WITH preview AS MATERIALIZED (`+ownedBindingIDSelect+scope+` ORDER BY `+order+` LIMIT 3)`+ownedBindingSelect+` JOIN preview ON preview.id=t.id ORDER BY `+order, args...)
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
	page := []Endpoint{endpoint}
	if err := attachEndpointBrowseTx(ctx, tx, userID, page); err != nil {
		return nil, err
	}
	return page[0].Browse, nil
}

func endpointKeyBrowseTx(ctx context.Context, tx *sql.Tx, userID, endpointID int64, key EndpointKey) (*EndpointKeyBrowse, error) {
	page := []EndpointKey{key}
	if err := attachEndpointKeyBrowseTx(ctx, tx, userID, endpointID, page); err != nil {
		return nil, err
	}
	return page[0].Browse, nil
}

// Only models on this page participate. Counts and the first three bindings
// share the page transaction's ownership checks and snapshot.
func attachModelBrowseTx(ctx context.Context, tx *sql.Tx, userID int64, models []Model) error {
	if len(models) == 0 {
		return nil
	}
	args := []any{userID, userID}
	summaries := make(map[string]*ModelBrowse, len(models))
	for i := range models {
		models[i].Browse = &ModelBrowse{AvailableBindingCount: "0", Preview: make([]KeyBindingView, 0, 3)}
		summaries[models[i].ID] = models[i].Browse
		args = append(args, models[i].ID)
	}
	scope := ownedBindingScope + ` AND m.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(models)), ",") + `)`
	rows, err := tx.QueryContext(ctx, `SELECT m.id,COUNT(DISTINCT CASE WHEN `+ownedBindingState+`='available' THEN b.id ELSE NULL END)`+ownedBindingJoin+scope+` GROUP BY m.id`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var count int64
		if err = rows.Scan(&id, &count); err != nil {
			rows.Close()
			return err
		}
		summaries[id].AvailableBindingCount = strconv.FormatInt(count, 10)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Rank only this page's owned bindings. Hydrate the bounded preview after
	// ranking, so eligibility and labels are read for at most three per model.
	rows, err = tx.QueryContext(ctx, `WITH preview AS (SELECT t.id,ROW_NUMBER() OVER (PARTITION BY m.id ORDER BY b.ord,b.id,t.ord,t.id) AS position`+ownedBindingJoin+scope+`)`+ownedBindingSelect+` JOIN preview ON preview.id=t.id AND preview.position<=3 ORDER BY m.id,b.ord,b.id,t.ord,t.id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanKeyBinding(rows)
		if err != nil {
			return err
		}
		summary := summaries[item.ModelID]
		summary.Preview = append(summary.Preview, item)
	}
	return rows.Err()
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
			scope += ` AND t.upstream_model_id=?`
			args = append(args, upstreamModel)
		}
		const order = "t.upstream_model_id,b.id,t.id"
		// Count and order only target identities. Hydrate the selected page in
		// this same snapshot and deadline, without evaluating skipped states.
		selected, err := readNumberedPage(ctx, tx, page, ownedBindingIDSelect+scope, order, "id", args, func(s pageScanner) (int64, error) {
			var targetID int64
			err := s.Scan(&targetID)
			return targetID, err
		})
		if err != nil {
			return Page[KeyBindingView]{}, err
		}
		result := Page[KeyBindingView]{Data: make([]KeyBindingView, 0, len(selected.Data)), Pagination: selected.Pagination}
		if len(selected.Data) == 0 {
			return result, nil
		}
		for _, targetID := range selected.Data {
			args = append(args, targetID)
		}
		rows, err := tx.QueryContext(ctx, ownedBindingSelect+scope+` AND t.id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(selected.Data)), ",")+`) ORDER BY `+order, args...)
		if err != nil {
			return Page[KeyBindingView]{}, err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanKeyBinding(rows)
			if err != nil {
				return Page[KeyBindingView]{}, err
			}
			result.Data = append(result.Data, item)
		}
		return result, rows.Err()
	})
}
