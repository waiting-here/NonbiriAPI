package adminusers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const routeUserPenalties = "/admin/api/users/{id}/penalties"

// Separate envelopes keep this explicitly shared management projection out of
// the owner/session DTOs, which never contain statistical evidence.
type adminPenaltyResponse struct{ antiabuse.CasePage }
type stewardPenaltyResponse struct{ antiabuse.CasePage }
type adminPenaltyDetailResponse struct{ antiabuse.CaseDetail }
type stewardPenaltyDetailResponse struct{ antiabuse.CaseDetail }
type adminPenaltyEvidenceResponse struct{ antiabuse.EvidencePage }
type stewardPenaltyEvidenceResponse struct{ antiabuse.EvidencePage }

func (api *httpAPI) getPenalties(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	if !requireNoBody(w, r) {
		return
	}
	user, ok := pathUserID(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	fields := []string{"page", "page_size"}
	if r.PathValue("caseId") == "" {
		fields = append(fields, "type", "state")
	}
	values, ok := strictQuery(w, r, fields...)
	if !ok {
		return
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	tx, err := api.service.beginManagement(r.Context(), p.UserID, api.role, false)
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback()
	now := api.service.now().Unix()
	var result any
	if id := r.PathValue("caseId"); id != "" {
		if raw := r.PathValue("actionId"); raw != "" {
			action, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || action <= 0 || strconv.FormatInt(action, 10) != raw {
				writeError(w, ErrNotFound)
				return
			}
			var evidence antiabuse.EvidencePage
			evidence, err = antiabuse.ReadEvidenceTx(r.Context(), tx, user, now, id, action, page)
			if api.role == roleSteward {
				result = stewardPenaltyEvidenceResponse{evidence}
			} else {
				result = adminPenaltyEvidenceResponse{evidence}
			}
		} else {
			var detail antiabuse.CaseDetail
			detail, err = antiabuse.ReadCaseTx(r.Context(), tx, user, now, id, page)
			if api.role == roleSteward {
				result = stewardPenaltyDetailResponse{detail}
			} else {
				result = adminPenaltyDetailResponse{detail}
			}
		}
	} else {
		var cases antiabuse.CasePage
		cases, err = antiabuse.ReadCasesTx(r.Context(), tx, user, now, page, values.Get("type"), values.Get("state"))
		if api.role == roleSteward {
			result = stewardPenaltyResponse{cases}
		} else {
			result = adminPenaltyResponse{cases}
		}
	}
	if err != nil {
		if errors.Is(err, antiabuse.ErrNotFound) {
			err = ErrNotFound
		} else if errors.Is(err, antiabuse.ErrInvalid) {
			err = ErrInvalidRequest
		}
		writeError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, ErrUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
