package ranking

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func RegisterRoutes(registrar resources.UserRouteRegistrar, s *Service) error {
	if registrar == nil || s == nil {
		return ErrInvalid
	}
	for _, route := range []struct{ path, board string }{
		{"/api/charity/leaderboard", "charity"},
		{"/api/games/leaderboards/charity", "game_charity"},
		{"/api/games/bidding/leaderboard", "bidding"},
		{"/api/games/blackjack/leaderboard", "blackjack"},
		{"/api/games/leaderboards/net-profit", "game_net_profit"},
		{"/api/games/fishing/net-profit", "fishing_net_profit"},
		{"/api/games/blackjack/net-profit", "blackjack_net_profit"},
	} {
		if err := registrar.RegisterUserRoute(http.MethodGet, route.path, func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
			if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
				writeError(w, ErrInvalid)
				return
			}
			values, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil {
				writeError(w, ErrInvalid)
				return
			}
			window := "7d"
			page := pagination.Default()
			for key, list := range values {
				if len(list) != 1 || !(route.board == "charity" && key == "page" || (route.board == "bidding" || route.board == "blackjack" || isNetBoard(route.board)) && key == "window") {
					writeError(w, ErrInvalid)
					return
				}
			}
			if route.board == "charity" {
				window = "history"
				page, _, err = pagination.Parse(values)
			} else if value, ok := values["window"]; ok {
				window = value[0]
			}
			if err != nil || !validBoard(route.board, window) {
				writeError(w, ErrInvalid)
				return
			}
			result, err := s.Read(r.Context(), p.UserID, route.board, window, page)
			if err != nil {
				writeError(w, err)
				return
			}
			httperr.WriteJSON(w, http.StatusOK, result)
		}); err != nil {
			return err
		}
	}
	return nil
}

func writeError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeServiceUnavailable, "rankings temporarily unavailable"
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "invalid leaderboard request"
	case errors.Is(err, resources.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, resources.ErrForbidden):
		code, message = httperr.CodeForbidden, "access denied"
	case errors.Is(err, resources.ErrNotFound):
		code, message = httperr.CodeNotFound, "not found"
	case errors.Is(err, resources.ErrMaintenance):
		code, message = httperr.CodeMaintenance, "maintenance in progress"
	}
	if code == httperr.CodeServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	httperr.WriteError(w, httperr.New(code, message))
}
