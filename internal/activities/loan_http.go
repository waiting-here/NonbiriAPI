package activities

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const (
	routeLoanQuote = "/api/activities/loan/quote"
	routeLoan      = "/api/activities/loan"
	routeLoans     = "/api/activities/loans"
)

func (api *httpAPI) quoteLoan(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
	}
	var body struct {
		Tier requestField[string] `json:"tier"`
	}
	if !decodeStrictObject(w, r, &body) {
		return
	}
	if !body.Tier.Set {
		writeActivitiesError(w, ErrInvalidRequest)
		return
	}
	value, err := api.service.repository.QuoteLoan(r.Context(), p.UserID, body.Tier.Value)
	if err != nil {
		writeLoanError(w, r, err)
		return
	}
	writeActivitiesJSON(w, http.StatusOK, value)
}

func (api *httpAPI) borrow(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
	}
	var body struct {
		Token requestField[string] `json:"quote_token"`
	}
	if !decodeStrictObject(w, r, &body) {
		return
	}
	if !body.Token.Set || len(body.Token.Value) == 0 || len(body.Token.Value) > maxLoanTokenBytes {
		writeActivitiesError(w, ErrInvalidRequest)
		return
	}
	mutation, ok := bodyControlMutation(w, r, routeLoan, nil, map[string]any{"quote_token": body.Token.Value})
	if !ok {
		return
	}
	result, err := api.service.Borrow(r.Context(), p.UserID, mutation, body.Token.Value)
	if err != nil {
		writeLoanError(w, r, err)
		return
	}
	writeActivitiesMutation(w, result.Status, result.Body)
}

func (api *httpAPI) listLoans(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := strictQuery(w, r)
	if !ok {
		return
	}
	if !exactQuery(values, "page", "page_size") {
		writeActivitiesError(w, ErrInvalidRequest)
		return
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeActivitiesError(w, ErrInvalidRequest)
		return
	}
	value, err := api.service.repository.ListLoans(r.Context(), p.UserID, page)
	if err != nil {
		writeLoanError(w, r, err)
		return
	}
	writeActivitiesJSON(w, http.StatusOK, value)
}

func writeLoanError(w http.ResponseWriter, r *http.Request, err error) {
	// Retirement cancels admitted requests before draining them. SQLite can
	// report that cancellation as SQLITE_INTERRUPT rather than context.Canceled.
	if r.Context().Err() != nil {
		err = ErrUnavailable
	}
	writeActivitiesError(w, err)
}
