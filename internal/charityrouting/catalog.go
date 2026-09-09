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
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type CatalogModel struct {
	CapabilityModel
	PublicDescription  string `json:"public_description"`
	Enabled            bool   `json:"enabled"`
	AllowedLevels      []int  `json:"allowed_levels"`
	LevelAllowed       bool   `json:"level_allowed"`
	CurrentlyAvailable bool   `json:"currently_available"`
	Availability       string `json:"availability"`
}

type Catalog struct {
	Models         []CatalogModel      `json:"models"`
	Pagination     pagination.Metadata `json:"pagination"`
	DonationIntake string              `json:"donation_intake"`
	ServerNow      int64               `json:"server_now"`
}

type CatalogFilter struct {
	Query              string
	AllowedForMe       *bool
	AllowedLevel       *int
	CurrentlyAvailable *bool
}

func (s *Service) Catalog(ctx context.Context, userID int64, filter CatalogFilter, page pagination.Request) (Catalog, error) {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 || !page.Valid() ||
		!utf8.ValidString(filter.Query) || utf8.RuneCountInString(filter.Query) > 128 ||
		len(filter.Query) > 512 || strings.ContainsRune(filter.Query, 0) ||
		(filter.AllowedLevel != nil && (*filter.AllowedLevel < 1 || *filter.AllowedLevel > 5)) {
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
	var tokenReserveText string
	if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM charity_models WHERE pricing_mode='per_token')
THEN (SELECT value FROM site_config WHERE key='charity_token_reserve_milli') ELSE '1' END`).Scan(&tokenReserveText); err != nil {
		return Catalog{}, fmt.Errorf("charity routing: read catalog reserve: %w", err)
	}
	tokenReserve, err := strconv.ParseInt(tokenReserveText, 10, 64)
	if err != nil || tokenReserve < 1 || tokenReserve > db.MaxMoneyMilli {
		return Catalog{}, ErrInvariant
	}
	from := ` FROM charity_models cm JOIN charity_model_access a ON a.model_id=cm.id
CROSS JOIN (SELECT ? AS decision_now,? AS token_reserve,? AS charity_enabled) cx WHERE 1=1`
	args := []any{now, tokenReserve, charityGate == "1"}
	if filter.Query != "" {
		from += ` AND (instr(lower(cm.full_name),lower(?))>0 OR instr(lower(a.public_description),lower(?))>0)`
		args = append(args, filter.Query, filter.Query)
	}
	if filter.AllowedForMe != nil {
		from += ` AND ((a.allowed_level_mask & ?)<>0)=?`
		args = append(args, 1<<(level-1), *filter.AllowedForMe)
	}
	if filter.AllowedLevel != nil {
		from += ` AND (a.allowed_level_mask & ?)<>0`
		args = append(args, 1<<(*filter.AllowedLevel-1))
	}
	if filter.CurrentlyAvailable != nil {
		from += ` AND ` + catalogAvailableSQL() + `=?`
		args = append(args, *filter.CurrentlyAvailable)
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
cm.enabled,a.allowed_level_mask,a.public_description,`+catalogAvailableSQL()+from+` ORDER BY cm.full_name,cm.id LIMIT ? OFFSET ?`,
		append(args, page.Size, offset)...)
	if err != nil {
		return Catalog{}, fmt.Errorf("charity routing: read catalog page: %w", err)
	}
	models := make([]CatalogModel, 0, page.Size)
	for rows.Next() {
		model, _, err := scanCatalogModel(rows, level)
		if err != nil {
			_ = rows.Close()
			return Catalog{}, err
		}
		models = append(models, model)
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
		case model.CurrentlyAvailable:
			model.Availability = "available"
		default:
			model.Availability = "no_usable_key"
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
		&discountEnabled, &model.Discount.Percent, &start, &end, &enabled, &mask, &model.PublicDescription, &model.CurrentlyAvailable); err != nil {
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
	if !exactQuery(values, "view", "page", "page_size", "q", "allowed_for_me", "allowed_level", "currently_available") || values.Get("view") != "catalog" {
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
	_, present = values["currently_available"]
	available, err := parseEnabledFilter(values.Get("currently_available"), present)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	filter := CatalogFilter{Query: values.Get("q"), AllowedForMe: allowed, CurrentlyAvailable: available}
	if entries, present := values["allowed_level"]; present {
		if len(entries) != 1 || len(entries[0]) != 1 || entries[0][0] < '1' || entries[0][0] > '5' {
			writeRoutingError(writer, ErrInvalidRequest)
			return
		}
		level := int(entries[0][0] - '0')
		filter.AllowedLevel = &level
	}
	value, err := api.service.Catalog(request.Context(), principal.UserID, filter, page)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeJSON(writer, value)
}
