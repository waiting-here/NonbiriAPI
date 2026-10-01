package charityrouting

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type AutomationPage[T any] struct {
	Data       []T                  `json:"data"`
	Pagination *pagination.Metadata `json:"pagination"`
}

type AutomationBinding struct {
	AdminBinding
	State string `json:"state"`
}
type AutomationBindings struct {
	AutomationPage[AutomationBinding]
	BindingRevision string `json:"binding_revision"`
}

func (s *Service) AutomationBindings(ctx context.Context, userID, modelID int64, q string, page pagination.Request) (AutomationBindings, error) {
	out := AutomationBindings{AutomationPage: AutomationPage[AutomationBinding]{Data: []AutomationBinding{}}}
	tx, _, err := s.beginManagementTx(ctx, roleSteward, userID, modelID, false, true)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	current, err := readAdminBindingsTx(ctx, tx, modelID)
	if err != nil {
		return out, err
	}
	out.BindingRevision = current.BindingRevision
	matching := make([]AdminBinding, 0, len(current.Bindings))
	for _, item := range current.Bindings {
		if q == "" || strings.Contains(strings.ToLower(item.UpstreamModelID+" "+item.Source.CanonicalBaseURL), strings.ToLower(q)) {
			matching = append(matching, item)
		}
	}
	meta, offset, err := page.Window(int64(len(matching)))
	if err != nil {
		return out, ErrInvalidRequest
	}
	out.Pagination = &meta
	now, err := s.nowUnix()
	if err != nil {
		return out, err
	}
	gate, err := capabilityGateTx(ctx, tx, "charity_enabled")
	if err != nil {
		return out, err
	}
	var reserveText sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_token_reserve_milli'`).Scan(&reserveText)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	reserve := int64(0)
	if reserveText.Valid {
		reserve, err = strconv.ParseInt(reserveText.String, 10, 64)
		if err != nil {
			return out, err
		}
	}
	end := int(offset) + page.Size
	if end > len(matching) {
		end = len(matching)
	}
	for _, item := range matching[int(offset):end] {
		var state string
		query := `SELECT ` + keyModelStateSQL() + keyModelFrom + ` AND rb.id=?`
		err = tx.QueryRowContext(ctx, query, now, reserve, gate == "1", item.DonationID, item.DonationKeyID, item.ID).Scan(&state)
		if err != nil {
			return out, err
		}
		out.Data = append(out.Data, AutomationBinding{AdminBinding: item, State: state})
	}
	return out, tx.Commit()
}
func (s *Service) AutomationCandidates(ctx context.Context, userID, modelID int64, q string, page pagination.Request) (AutomationPage[AdminBindingCandidate], error) {
	value, err := s.candidatesPage(ctx, roleSteward, userID, modelID, CandidateQuery{Query: q}, page)
	return AutomationPage[AdminBindingCandidate]{Data: value.Data, Pagination: value.Pagination}, err
}

type AutomationCatalog struct {
	AutomationPage[ManualCandidate]
	ManualCatalogRevision string `json:"manual_catalog_revision"`
	DiscoveryRevision     string `json:"discovery_revision"`
	DiscoveryState        string `json:"discovery_state"`
}

func (s *Service) AutomationCatalog(ctx context.Context, userID, donationID, keyID int64, q string, page pagination.Request) (AutomationCatalog, error) {
	out := AutomationCatalog{}
	tx, scope, err := s.beginManagementTx(ctx, roleSteward, userID, 0, false, true)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return out, err
	}
	if err = scope.RequireKey(ctx, tx, donationID, keyID, now, false); err != nil {
		return out, managementScopeError(err)
	}
	var owned bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_keys WHERE id=? AND donation_id=?)`, keyID, donationID).Scan(&owned)
	if err != nil {
		return out, err
	}
	if !owned {
		return out, ErrNotFound
	}
	out.Data, out.Pagination, out.ManualCatalogRevision, err = readManualCandidatesTx(ctx, tx, keyID, q, page)
	if err != nil {
		return out, err
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(e.revision,1),COALESCE(e.state,'unknown') FROM donation_keys dk LEFT JOIN model_discovery_evidence e ON e.endpoint_key_id=dk.endpoint_key_id WHERE dk.id=?`, keyID).Scan(&revision, &out.DiscoveryState)
	if err != nil {
		return out, err
	}
	out.DiscoveryRevision = strconv.FormatInt(revision, 10)
	return out, tx.Commit()
}

func (s *Service) AutomationModels(ctx context.Context, actorID int64, query string, page pagination.Request) (AutomationPage[StewardCharityModel], error) {
	var empty AutomationPage[StewardCharityModel]
	if ctx == nil || !page.Valid() || !utf8.ValidString(query) || utf8.RuneCountInString(query) > 200 {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginPageRead(ctx, roleSteward, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	scope, err := s.managementScope(ctx, tx, roleSteward, actorID, 0, true)
	if err != nil {
		return empty, err
	}
	selection := `SELECT id FROM charity_models WHERE 1=1`
	if scope.Trainee {
		selection += ` AND is_mainstream=1`
	}
	args := []any{}
	if query != "" {
		selection += ` AND (provider LIKE ? ESCAPE '\' OR model LIKE ? ESCAPE '\' OR full_name LIKE ? ESCAPE '\')`
		pattern := "%" + escapeLike(query) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	metadata, offset, err := pageWindow(ctx, tx, selection, args, page)
	if err != nil {
		return empty, err
	}
	rows, err := tx.QueryContext(ctx, selection+` ORDER BY id LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return empty, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return empty, err
	}
	result := AutomationPage[StewardCharityModel]{Data: make([]StewardCharityModel, 0, len(ids)), Pagination: &metadata}
	for _, id := range ids {
		item, err := getAdminModelTx(ctx, tx, id)
		if err != nil {
			return empty, err
		}
		result.Data = append(result.Data, stewardModel(item))
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}
