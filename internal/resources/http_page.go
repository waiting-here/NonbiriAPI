package resources

import (
	"context"
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Return false only when the legacy handler should interpret its own cursor
// query. Even empty cursor/limit parameters cannot be mixed with page mode.
func serveNumberedPage[T any](writer http.ResponseWriter, request *http.Request, fields []string, read func(context.Context, url.Values, pagination.Request) (T, error)) bool {
	values, ok := requestQuery(writer, request)
	if !ok {
		return true
	}
	page, mode, err := pagination.Parse(values)
	if err != nil {
		writeResourceError(writer, ErrInvalidRequest)
		return true
	}
	if !mode {
		return false
	}
	if !exactQuery(values, append([]string{"page", "page_size"}, fields...)...) {
		writeResourceError(writer, ErrInvalidRequest)
		return true
	}
	value, err := read(request.Context(), values, page)
	if err != nil {
		writeResourceError(writer, err)
	} else {
		writeJSON(writer, http.StatusOK, value)
	}
	return true
}
