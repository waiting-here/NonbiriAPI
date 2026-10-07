package duel

import (
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/game/rating"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) registerRankingRoute(registrar resources.UserRouteRegistrar) error {
	if s.descriptor.ResolveBoard("wins") != nil {
		return nil
	}
	return registrar.RegisterUserRoute("GET", "/api/games/"+s.rules.ID()+"/leaderboard", func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
		if !noBody(w, r) {
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(q) > 1 || len(q["window"]) > 1 || len(q) == 1 && len(q["window"]) != 1 {
			writeError(w, ErrInvalidRequest)
			return
		}
		window := q.Get("window")
		if window == "" {
			window = "7d"
		}
		if window != "7d" && window != "30d" {
			writeError(w, ErrInvalidRequest)
			return
		}
		tx, now, err := s.beginRead(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback()
		if err := s.authorize(r.Context(), tx, Identity{UserID: p.UserID}); err != nil {
			writeError(w, err)
			return
		}
		board, err := rating.ReadTx(r.Context(), tx, s.rules.ID(), p.UserID, window, now)
		writeValue(w, board, err)
	})
}
