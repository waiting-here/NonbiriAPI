package adminusers

import (
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const routeUserLoans = "/admin/api/users/{id}/loans"

func (api *httpAPI) getLoans(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	if !requireNoBody(w, r) {
		return
	}
	user, ok := pathUserID(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	values, ok := strictQuery(w, r, "page", "page_size")
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
	result, err := activities.ReadLoansTx(r.Context(), tx, user, page)
	if err != nil {
		if errors.Is(err, activities.ErrNotFound) {
			err = ErrNotFound
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
