package charityrouting

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const (
	routeAdminKeyModels          = "/admin/api/donations/{id}/keys/{keyId}/models"
	routeAdminKeyModelBindings   = routeAdminKeyModels + "/{modelId}/bindings"
	routeStewardKeyModels        = "/api/steward/donations/{id}/keys/{keyId}/models"
	routeStewardKeyModelBindings = routeStewardKeyModels + "/{modelId}/bindings"
)

// Both roles receive only this common, safe association projection.
type KeyModel struct {
	ModelID               string `json:"model_id"`
	FullName              string `json:"full_name"`
	Enabled               bool   `json:"enabled"`
	BindingCount          string `json:"binding_count"`
	AvailableBindingCount string `json:"available_binding_count"`
}

type KeyModelBinding struct {
	BindingID       string `json:"binding_id"`
	UpstreamModelID string `json:"upstream_model_id"`
	Ord             int    `json:"ord"`
	State           string `json:"state"`
}

// Left joins retain a still-configured association even when its source has
// become ineligible. Availability reuses the public charity routing predicate.
const keyModelFrom = ` FROM charity_model_bindings rb
JOIN charity_models cm ON cm.id=rb.charity_model_id
JOIN donation_keys rk ON rk.id=rb.donation_key_id
JOIN donations rd ON rd.id=rk.donation_id
LEFT JOIN endpoint_keys pk ON pk.id=rb.endpoint_key_id
LEFT JOIN endpoints pe ON pe.id=pk.endpoint_id
CROSS JOIN (SELECT ? AS decision_now,? AS token_reserve,? AS charity_enabled) cx
WHERE rk.donation_id=? AND rk.id=?`

func keyModelStateSQL() string {
	return `CASE
WHEN rk.ended_at IS NOT NULL THEN 'ended'
WHEN (rk.expires_at IS NOT NULL AND rk.expires_at<=cx.decision_now) OR rd.status='expired' THEN 'expired'
WHEN rd.status<>'approved' THEN 'pending'
WHEN cx.charity_enabled=0 THEN 'feature_disabled'
WHEN cm.enabled=0 THEN 'model_disabled'
WHEN rk.enabled=0 OR pk.enabled=0 OR pe.enabled=0 THEN 'disabled'
WHEN rk.failure_disabled=1 OR EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=rb.endpoint_key_id) THEN 'suspended'
WHEN ` + catalogAvailableBindingSQL("rb.id") + ` THEN 'available'
ELSE 'unavailable' END`
}

func (s *Service) keyModelPages(ctx context.Context, role roleKind, actorID, donationID, keyID, modelID int64, page pagination.Request) (Page[KeyModel], Page[KeyModelBinding], error) {
	var models Page[KeyModel]
	var bindings Page[KeyModelBinding]
	if ctx == nil || donationID <= 0 || keyID <= 0 || modelID < 0 || !page.Valid() {
		return models, bindings, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginPageRead(ctx, role, actorID)
	if err != nil {
		return models, bindings, err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_keys k JOIN donations d ON d.id=k.donation_id WHERE d.id=? AND k.id=?)`, donationID, keyID).Scan(&exists); err != nil {
		return models, bindings, err
	}
	if !exists {
		return models, bindings, ErrNotFound
	}
	if modelID != 0 {
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_model_bindings WHERE charity_model_id=? AND donation_key_id=?)`, modelID, keyID).Scan(&exists); err != nil {
			return models, bindings, err
		}
		if !exists {
			return models, bindings, ErrNotFound
		}
	}
	now, err := s.nowUnix()
	if err != nil {
		return models, bindings, err
	}
	gate, err := capabilityGateTx(ctx, tx, "charity_enabled")
	if err != nil {
		return models, bindings, err
	}
	var reserveText sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_token_reserve_milli'`).Scan(&reserveText); err != nil && err != sql.ErrNoRows {
		return models, bindings, err
	}
	var reserve int64
	if reserveText.Valid {
		reserve, err = strconv.ParseInt(reserveText.String, 10, 64)
		if err != nil || reserve < 1 || reserve > db.MaxMoneyMilli {
			return models, bindings, ErrInvariant
		}
	}
	args := []any{now, reserve, gate == "1", donationID, keyID}
	selection := `SELECT cm.id,cm.full_name,cm.enabled,COUNT(*),SUM(CASE WHEN (` + keyModelStateSQL() + `)='available' THEN 1 ELSE 0 END)` + keyModelFrom + ` GROUP BY cm.id,cm.full_name,cm.enabled`
	order := "cm.full_name,cm.id"
	if modelID != 0 {
		selection = `SELECT rb.id,rb.upstream_model_id,rb.ord,` + keyModelStateSQL() + keyModelFrom + ` AND cm.id=?`
		args = append(args, modelID)
		order = "rb.ord,rb.id"
	}
	meta, offset, err := pageWindow(ctx, tx, selection, args, page)
	if err != nil {
		return models, bindings, err
	}
	rows, err := tx.QueryContext(ctx, selection+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return models, bindings, err
	}
	defer rows.Close()
	models = Page[KeyModel]{Data: make([]KeyModel, 0), Pagination: &meta}
	bindings = Page[KeyModelBinding]{Data: make([]KeyModelBinding, 0), Pagination: &meta}
	for rows.Next() {
		var id int64
		if modelID == 0 {
			var value KeyModel
			var count, available int64
			if err = rows.Scan(&id, &value.FullName, &value.Enabled, &count, &available); err != nil {
				return models, bindings, err
			}
			value.ModelID, value.BindingCount, value.AvailableBindingCount = strconv.FormatInt(id, 10), strconv.FormatInt(count, 10), strconv.FormatInt(available, 10)
			models.Data = append(models.Data, value)
		} else {
			var value KeyModelBinding
			if err = rows.Scan(&id, &value.UpstreamModelID, &value.Ord, &value.State); err != nil {
				return models, bindings, err
			}
			value.BindingID = strconv.FormatInt(id, 10)
			bindings.Data = append(bindings.Data, value)
		}
	}
	if err = rows.Err(); err != nil {
		return models, bindings, err
	}
	if err = rows.Close(); err != nil {
		return models, bindings, err
	}
	if err = tx.Commit(); err != nil {
		return models, bindings, err
	}
	return models, bindings, nil
}

func (api *httpAPI) adminKeyModels(w http.ResponseWriter, r *http.Request) {
	api.keyModels(w, r, roleAdmin, 0, false)
}
func (api *httpAPI) adminKeyModelBindings(w http.ResponseWriter, r *http.Request) {
	api.keyModels(w, r, roleAdmin, 0, true)
}
func (api *httpAPI) stewardKeyModels(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.keyModels(w, r, roleSteward, p.UserID, false)
}
func (api *httpAPI) stewardKeyModelBindings(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.keyModels(w, r, roleSteward, p.UserID, true)
}
func (api *httpAPI) keyModels(w http.ResponseWriter, r *http.Request, role roleKind, actorID int64, expanded bool) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := requestQuery(w, r)
	if !ok {
		return
	}
	for name, entries := range values {
		if (name != "page" && name != "page_size") || len(entries) != 1 {
			writeRoutingError(w, ErrInvalidRequest)
			return
		}
	}
	page, mode, err := pagination.Parse(values)
	if err != nil {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	if !mode {
		page = pagination.Default()
	}
	donationID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, r, "keyId")
	if !ok {
		return
	}
	var modelID int64
	if expanded {
		modelID, ok = parsePathID(w, r, "modelId")
		if !ok {
			return
		}
	}
	models, bindings, err := api.service.keyModelPages(r.Context(), role, actorID, donationID, keyID, modelID, page)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	if expanded {
		writeJSON(w, bindings)
	} else {
		writeJSON(w, models)
	}
}
