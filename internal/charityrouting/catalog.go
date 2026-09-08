package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/charityaccess"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type CatalogModel struct {
	CapabilityModel
	PublicDescription string `json:"public_description"`
	Enabled           bool   `json:"enabled"`
	AllowedLevels     []int  `json:"allowed_levels"`
	LevelAllowed      bool   `json:"level_allowed"`
	Availability      string `json:"availability"`
}

type Catalog struct {
	Models         []CatalogModel      `json:"models"`
	Pagination     pagination.Metadata `json:"pagination"`
	DonationIntake string              `json:"donation_intake"`
	ServerNow      int64               `json:"server_now"`
}

type CatalogFilter struct {
	Query        string
	AllowedForMe *bool
}

func (s *Service) Catalog(ctx context.Context, userID int64, filter CatalogFilter, page pagination.Request) (Catalog, error) {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 || !page.Valid() ||
		!utf8.ValidString(filter.Query) || utf8.RuneCountInString(filter.Query) > 128 ||
		len(filter.Query) > 512 || strings.ContainsRune(filter.Query, 0) {
		return Catalog{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Catalog{}, fmt.Errorf("charity routing: begin catalog: %w", err)
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return Catalog{}, err
	}
	level, err := charityaccess.CurrentLevel(ctx, tx, userID)
	if err != nil {
		if errors.Is(err, charityaccess.ErrUnavailable) {
			return Catalog{}, ErrUnauthorized
		}
		return Catalog{}, err
	}
	charityGate, err := capabilityGateTx(ctx, tx, "charity_enabled")
	if err != nil {
		return Catalog{}, err
	}
	donationGate, err := capabilityGateTx(ctx, tx, "donation_accept_enabled")
	if err != nil {
		return Catalog{}, err
	}
	if charityGate == "0" && donationGate == "1" {
		return Catalog{}, ErrInvariant
	}
	from := ` FROM charity_models cm JOIN charity_model_access a ON a.model_id=cm.id WHERE 1=1`
	args := make([]any, 0, 5)
	if filter.Query != "" {
		from += ` AND (instr(lower(cm.full_name),lower(?))>0 OR instr(lower(a.public_description),lower(?))>0)`
		args = append(args, filter.Query, filter.Query)
	}
	if filter.AllowedForMe != nil {
		from += ` AND ((a.allowed_level_mask & ?)<>0)=?`
		args = append(args, 1<<(level-1), *filter.AllowedForMe)
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return Catalog{}, fmt.Errorf("charity routing: count catalog: %w", err)
	}
	meta, offset, err := page.Window(total)
	if err != nil {
		return Catalog{}, ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, `SELECT cm.id,cm.provider,cm.model,cm.full_name,cm.pricing_mode,
cm.request_user_price,cm.uncached_user_price,cm.cache_write_user_price,cm.cache_read_user_price,cm.output_user_price,
cm.discount_enabled,cm.discount_percent,cm.discount_start_at,cm.discount_end_at,
cm.enabled,a.allowed_level_mask,a.public_description`+from+` ORDER BY cm.full_name,cm.id LIMIT ? OFFSET ?`,
		append(args, page.Size, offset)...)
	if err != nil {
		return Catalog{}, fmt.Errorf("charity routing: read catalog page: %w", err)
	}
	models := make([]CatalogModel, 0, page.Size)
	ids := make([]int64, 0, page.Size)
	for rows.Next() {
		model, id, err := scanCatalogModel(rows, level)
		if err != nil {
			_ = rows.Close()
			return Catalog{}, err
		}
		models, ids = append(models, model), append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Catalog{}, fmt.Errorf("charity routing: iterate catalog: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Catalog{}, fmt.Errorf("charity routing: close catalog: %w", err)
	}
	for index := range models {
		model := &models[index]
		switch {
		case charityGate == "0":
			model.Availability = "feature_disabled"
		case !model.Enabled:
			model.Availability = "model_disabled"
		case !model.LevelAllowed:
			model.Availability = "level_denied"
		default:
			_, err := s.readSnapshotTx(ctx, tx, ids[index], now, false, nil)
			if err == nil {
				model.Availability = "available"
			} else if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) || errors.Is(err, donationquota.ErrLimited) {
				model.Availability = "no_usable_key"
			} else {
				return Catalog{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return Catalog{}, fmt.Errorf("charity routing: commit catalog: %w", err)
	}
	intake := "closed"
	if charityGate == "1" && donationGate == "1" {
		intake = "open"
	}
	return Catalog{Models: models, Pagination: meta, DonationIntake: intake, ServerNow: now}, nil
}

func scanCatalogModel(scanner interface{ Scan(...any) error }, level int) (CatalogModel, int64, error) {
	var model CatalogModel
	var id, requestPrice int64
	var mode string
	var tokenPrices [4]int64
	var enabled, discountEnabled, mask int
	var start, end sql.NullInt64
	if err := scanner.Scan(&id, &model.Provider, &model.Model, &model.FullName, &mode,
		&requestPrice, &tokenPrices[0], &tokenPrices[1], &tokenPrices[2], &tokenPrices[3],
		&discountEnabled, &model.Discount.Percent, &start, &end, &enabled, &mask, &model.PublicDescription); err != nil {
		return CatalogModel{}, 0, fmt.Errorf("charity routing: scan catalog model: %w", err)
	}
	model.ID = strconv.FormatInt(id, 10)
	model.Enabled = enabled == 1
	model.Discount.Enabled = discountEnabled == 1
	if start.Valid {
		model.Discount.StartAt = &start.Int64
	}
	if end.Valid {
		model.Discount.EndAt = &end.Int64
	}
	var err error
	model.AllowedLevels, err = charityaccess.Levels(mask)
	if err != nil {
		return CatalogModel{}, 0, ErrInvariant
	}
	model.LevelAllowed = charityaccess.Allows(mask, level)
	model.Pricing, err = capabilityPricing(mode, requestPrice, tokenPrices, model.Discount.Percent)
	if err != nil {
		return CatalogModel{}, 0, err
	}
	return model, id, nil
}

func (api *httpAPI) catalog(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	values, ok := requestQuery(writer, request)
	if !ok {
		return
	}
	if !exactQuery(values, "view", "page", "page_size", "q", "allowed_for_me") || values.Get("view") != "catalog" {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	_, present := values["allowed_for_me"]
	allowed, err := parseEnabledFilter(values.Get("allowed_for_me"), present)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	value, err := api.service.Catalog(request.Context(), principal.UserID, CatalogFilter{Query: values.Get("q"), AllowedForMe: allowed}, page)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeJSON(writer, value)
}
