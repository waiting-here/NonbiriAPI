package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/charitydiscount"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	routeAdminPriceTemplates   = "/admin/api/charity-price-templates"
	routeAdminPriceTemplate    = routeAdminPriceTemplates + "/{id}"
	routeStewardPriceTemplates = "/api/steward/charity-price-templates"
	routeStewardPriceTemplate  = routeStewardPriceTemplates + "/{id}"
	maxPriceTemplates          = 256
)

type PriceTemplate struct {
	RecurringDiscount   charitydiscount.Config `json:"recurring_discount"`
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description"`
	Enabled             bool                   `json:"enabled"`
	Revision            string                 `json:"revision"`
	Pricing             AdminPricing           `json:"pricing"`
	TokenReserveCredits *string                `json:"token_reserve_credits"`
	Discount            AdminDiscount          `json:"discount"`
	CreatedAt           int64                  `json:"created_at"`
	UpdatedAt           int64                  `json:"updated_at"`
}
type PriceTemplateInput struct {
	RecurringDiscount   *charitydiscount.Config
	Name                string
	Description         string
	Enabled             bool
	Pricing             PricingInput
	TokenReserveCredits *string
	Discount            DiscountInput
}

func normalizePriceTemplate(input PriceTemplateInput) (PriceTemplate, error) {
	var out PriceTemplate
	var err error
	out.RecurringDiscount, _, err = normalizeRecurring(input.RecurringDiscount)
	if err != nil {
		return out, err
	}
	input.Description = strings.ReplaceAll(input.Description, "\r\n", "\n")
	if !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 80 ||
		strings.TrimSpace(input.Name) != input.Name || !utf8.ValidString(input.Description) || utf8.RuneCountInString(input.Description) > 500 ||
		!validModelTokenReserve(input.TokenReserveCredits) || !validDiscount(input.Discount) {
		return out, ErrInvalidRequest
	}
	for _, r := range input.Name {
		if unicode.IsControl(r) {
			return out, ErrInvalidRequest
		}
	}
	for _, r := range input.Description {
		if unicode.IsControl(r) && r != 10 && r != 9 {
			return out, ErrInvalidRequest
		}
	}
	prices, err := validatePricing(input.Pricing)
	if err != nil {
		return out, ErrInvalidRequest
	}
	out.Name, out.Description, out.Enabled = input.Name, input.Description, input.Enabled
	out.Pricing.Mode = input.Pricing.Mode
	if input.Pricing.Mode == "per_request" {
		out.Pricing.UserPrice, out.Pricing.DonorReward = new(formatWireAmount(prices.requestUser)), new(formatWireAmount(prices.requestReward))
	} else {
		convert := func(v [4]int64) *AdminTokenPrices {
			return &AdminTokenPrices{formatWireAmount(v[0]), formatWireAmount(v[1]), formatWireAmount(v[2]), formatWireAmount(v[3])}
		}
		out.Pricing.UserPrices, out.Pricing.DonorRewards = convert(prices.user), convert(prices.reward)
	}
	if err = adminContextPricing(&out.Pricing, prices.contextPricing); err != nil {
		return out, err
	}
	if input.TokenReserveCredits != nil {
		n, _ := parseWireAmount(*input.TokenReserveCredits)
		out.TokenReserveCredits = new(formatWireAmount(n))
	}
	out.Discount = AdminDiscount{Enabled: input.Discount.Enabled, Percent: input.Discount.Percent, StartAt: copyInt(input.Discount.StartAt), EndAt: copyInt(input.Discount.EndAt)}
	return out, nil
}

func (s *Service) priceTemplates(ctx context.Context, role roleKind, actorID int64, query, state string, page pagination.Request) (Page[PriceTemplate], error) {
	var empty Page[PriceTemplate]
	if ctx == nil || !page.Valid() || !utf8.ValidString(query) || utf8.RuneCountInString(query) > 128 || strings.ContainsRune(query, 0) ||
		(state != "enabled" && state != "disabled" && state != "all") {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.beginRoleTx(ctx, role, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if role == roleSteward && state != "enabled" {
		return empty, ErrForbidden
	}
	selection, args := `SELECT `+priceTemplateColumns+` FROM charity_price_templates WHERE (instr(lower(name),lower(?))>0 OR instr(lower(description),lower(?))>0)`, []any{query, query}
	if state != "all" {
		selection += ` AND enabled=?`
		args = append(args, state == "enabled")
	}
	metadata, offset, err := pageWindow(ctx, tx, selection, args, page)
	if err != nil {
		return empty, err
	}
	rows, err := tx.QueryContext(ctx, selection+` ORDER BY id LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return empty, err
	}
	defer rows.Close()
	out := Page[PriceTemplate]{Data: make([]PriceTemplate, 0, page.Size), Pagination: &metadata}
	for rows.Next() {
		v, err := scanPriceTemplate(rows.Scan)
		if err != nil {
			return empty, err
		}
		out.Data = append(out.Data, v)
	}
	if err := rows.Err(); err != nil {
		return empty, err
	}
	return out, tx.Commit()
}

func (s *Service) priceTemplate(ctx context.Context, role roleKind, actorID, id int64) (PriceTemplate, error) {
	var empty PriceTemplate
	if ctx == nil || id <= 0 {
		return empty, ErrInvalidRequest
	}
	tx, _, err := s.beginRoleTx(ctx, role, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	out, err := readPriceTemplate(ctx, tx, id)
	if err != nil {
		return empty, err
	}
	if role == roleSteward && !out.Enabled {
		return empty, ErrNotFound
	}
	return out, tx.Commit()
}

const priceTemplateColumns = "id,name,description,enabled,revision,pricing,token_reserve_credits,discount,created_at,updated_at,recurring_discount"

func readPriceTemplate(ctx context.Context, tx *sql.Tx, id int64) (PriceTemplate, error) {
	return scanPriceTemplate(tx.QueryRowContext(ctx, "SELECT "+priceTemplateColumns+" FROM charity_price_templates WHERE id=?", id).Scan)
}

func scanPriceTemplate(scan func(...any) error) (PriceTemplate, error) {
	var out PriceTemplate
	var id, revision int64
	var pricing, discount, recurring string
	err := scan(&id, &out.Name, &out.Description, &out.Enabled, &revision, &pricing, &out.TokenReserveCredits, &discount, &out.CreatedAt, &out.UpdatedAt, &recurring)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal([]byte(pricing), &out.Pricing) != nil || json.Unmarshal([]byte(discount), &out.Discount) != nil {
		return out, ErrInvariant
	}
	out.RecurringDiscount, err = charitydiscount.Decode(recurring)
	if err != nil {
		return out, ErrInvariant
	}
	out.ID, out.Revision = strconv.FormatInt(id, 10), strconv.FormatInt(revision, 10)
	return out, nil
}

func (s *Service) mutatePriceTemplate(ctx context.Context, id int64, expected string, mutation resources.ControlMutation, input *PriceTemplateInput) (resources.MutationResult[PriceTemplate], error) {
	var empty resources.MutationResult[PriceTemplate]
	var value PriceTemplate
	var err error
	method, route := http.MethodPost, routeAdminPriceTemplates
	var ids []int64
	if id != 0 {
		method, route, ids = http.MethodPut, routeAdminPriceTemplate, []int64{id}
		if input == nil {
			method = http.MethodDelete
		}
	}
	if ctx == nil || !validMutation(mutation, method, route, ids...) {
		return empty, ErrInvalidRequest
	}
	var revision int64
	if id != 0 {
		revision, err = parsePositiveID(expected)
		if err != nil || revision == math.MaxInt64 {
			return empty, ErrInvalidRequest
		}
	}
	if input != nil {
		value, err = normalizePriceTemplate(*input)
		if err != nil {
			return empty, err
		}
	} else if id == 0 {
		return empty, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	tx, actorID, err := s.beginRoleTx(ctx, roleAdmin, 0)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	decision, err := beginMutation(ctx, tx, roleAdmin, actorID, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		result, err := replay[PriceTemplate](decision)
		if err != nil || result.Status == http.StatusNoContent {
			return result, err
		}
		if result.Value.RecurringDiscount.TimeZone == "" {
			result.Value.RecurringDiscount = charitydiscount.Default()
			result.Body, err = json.Marshal(result.Value)
			if err != nil {
				return empty, ErrInvariant
			}
		}
		return result, nil
	}
	action, status := "create", http.StatusCreated
	if id == 0 {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM charity_price_templates`).Scan(&count); err != nil {
			return empty, err
		}
		if count >= maxPriceTemplates {
			return empty, ErrResourceLimit
		}
	} else {
		previous, err := readPriceTemplate(ctx, tx, id)
		if err != nil {
			return empty, err
		}
		if previous.Revision != expected {
			return empty, ErrConflict
		}
		action, status = "replace", http.StatusOK
	}
	if input == nil {
		action, status = "delete", http.StatusNoContent
		if _, err = tx.ExecContext(ctx, `DELETE FROM charity_price_templates WHERE id=? AND revision=?`, id, revision); err != nil {
			return empty, err
		}
	} else {
		pricing, _ := json.Marshal(value.Pricing)
		discount, _ := json.Marshal(value.Discount)
		recurring, _ := charitydiscount.Encode(value.RecurringDiscount)
		if id == 0 {
			result, err := tx.ExecContext(ctx, `INSERT INTO charity_price_templates(name,description,enabled,revision,pricing,token_reserve_credits,discount,created_at,updated_at,recurring_discount) VALUES(?,?,?,1,?,?,?,?,?,?)`, value.Name, value.Description, value.Enabled, string(pricing), value.TokenReserveCredits, string(discount), now, now, recurring)
			if err != nil {
				return empty, err
			}
			id, err = result.LastInsertId()
			if err != nil {
				return empty, err
			}
		} else {
			result, err := tx.ExecContext(ctx, `UPDATE charity_price_templates SET name=?,description=?,enabled=?,revision=revision+1,pricing=?,token_reserve_credits=?,discount=?,updated_at=?,recurring_discount=? WHERE id=? AND revision=?`, value.Name, value.Description, value.Enabled, string(pricing), value.TokenReserveCredits, string(discount), now, recurring, id, revision)
			if err != nil {
				return empty, err
			}
			if err = requireOne(result); err != nil {
				return empty, err
			}
		}
		value, err = readPriceTemplate(ctx, tx, id)
		if err != nil {
			return empty, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO charity_price_template_audits(template_id,actor_user_id,action,from_revision,to_revision,created_at) VALUES(?,?,?,?,?,?)`, id, actorID, action, revision, revision+1, now); err != nil {
		return empty, err
	}
	var result resources.MutationResult[PriceTemplate]
	if status == http.StatusNoContent {
		err = idempotency.Complete(ctx, tx, decision, status, nil)
		result.Status = status
	} else {
		result, err = finishJSON(ctx, tx, decision, status, value)
	}
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}
