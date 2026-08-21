// Package checkin serves the user-station daily check-in API (frozen §I,
// implementation contract §6.3):
//
//	GET  /api/checkin   status projection (no writes at all)
//	POST /api/checkin   the atomic check-in
//
// Boundary rules:
//
//   - the station is and stays the USER station, and the principal is a user
//     session: the mount is wrapped by the shared user-session middleware and
//     each handler re-checks the station and the session principal (defense
//     in depth, mirroring the log rail). Caller keys, administrator sessions
//     and header-carried identities never authorize these routes;
//   - every refusal cause that a normal user must not distinguish — timezone
//     unset, mode disabled, a level-gated denial, a corrupt configuration —
//     is the SAME feature_disabled envelope (403) with the SAME message;
//   - the client never supplies the award, the day or any request body; a
//     POST with a non-empty body is invalid_request;
//   - all amounts on the wire are canonical decimal milli-credit strings.
package checkin

import (
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

// Deps wires the user-session middleware and the authoritative store.
type Deps struct {
	UserAuth *auth.UserAuth
	Store    *db.Store
}

// Handler is the mountable /api/checkin route tree.
type Handler struct {
	userAuth *auth.UserAuth
	store    *db.Store
	mux      *http.ServeMux
}

// New builds the route tree. The handlers re-check the station and session
// principal on every request (defense in depth on top of the middleware).
func New(deps Deps) *Handler {
	h := &Handler{userAuth: deps.UserAuth, store: deps.Store, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /api/checkin", h.status)
	h.mux.HandleFunc("POST /api/checkin", h.checkin)
	return h
}

// Handler returns the mountable route tree: user-session authentication (the
// shared auth.UserAuth middleware, which also enforces the user station)
// around the sub-tree, whose unmatched 404/405 fallbacks become the stable
// no-store JSON envelope through the shared httpmw.API wrapper.
func (h *Handler) Handler() http.Handler {
	if h == nil || h.userAuth == nil || h.store == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "check-in service unavailable"))
		})
	}
	return h.userAuth.Middleware(httpmw.API(h.mux))
}

// statusDisabledResp is the ENTIRE body while the feature is unavailable:
// no reason, no day, no range — timezone-unset must be indistinguishable from
// every other disabled cause.
type statusDisabledResp struct {
	Enabled bool `json:"enabled"`
}

// statusResp is the enabled status projection. All amounts are canonical
// decimal milli-credit strings (credits may carry "-").
type statusResp struct {
	Enabled         bool   `json:"enabled"`
	CheckedInToday  bool   `json:"checked_in_today"`
	Credits         string `json:"credits"`
	AwardMinMilli   string `json:"award_min_milli"`
	AwardMaxMilli   string `json:"award_max_milli"`
	CreditsCapMilli string `json:"credits_cap_milli"`
}

// checkinResp is the committed outcome of one successful check-in.
type checkinResp struct {
	AwardMilli string `json:"award_milli"`
	Credits    string `json:"credits"`
}

// requireUserSession enforces the user-station boundary and the user-session
// principal (mirrors the log rail; the mounting middleware already did both).
func (h *Handler) requireUserSession(w http.ResponseWriter, r *http.Request) (*db.User, bool) {
	if httpmw.StationOf(r) != host.StationUser {
		writeErr(w, httperr.New(httperr.CodeForbidden, "station authorization required"))
		return nil, false
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user == nil || user.ID <= 0 || !user.IsActive() {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return nil, false
	}
	return user, true
}

// status handles GET /api/checkin: the read-only availability/today
// projection. No query parameter is accepted.
func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "check-in service unavailable"))
		return
	}
	user, ok := h.requireUserSession(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	st, err := h.store.CheckinStatus(r.Context(), user.ID, time.Now())
	if err != nil {
		writeCheckinRepoErr(w, err)
		return
	}
	if !st.Enabled {
		httperr.WriteJSON(w, http.StatusOK, statusDisabledResp{Enabled: false})
		return
	}
	httperr.WriteJSON(w, http.StatusOK, statusResp{
		Enabled:         true,
		CheckedInToday:  st.CheckedInToday,
		Credits:         credits.FormatAmount(st.Credits),
		AwardMinMilli:   credits.FormatAmount(st.AwardMinMilli),
		AwardMaxMilli:   credits.FormatAmount(st.AwardMaxMilli),
		CreditsCapMilli: credits.FormatAmount(st.CreditsCapMilli),
	})
}

// checkin handles POST /api/checkin: the atomic daily check-in. The endpoint
// takes no input; a non-empty body is invalid_request.
func (h *Handler) checkin(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "check-in service unavailable"))
		return
	}
	user, ok := h.requireUserSession(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	// The client may never supply any part of the check-in (award, day, id):
	// any body at all is rejected rather than parsed.
	var probe [1]byte
	if n, _ := r.Body.Read(probe[:]); n > 0 {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	res, err := h.store.Checkin(r.Context(), user.ID, time.Now())
	if err != nil {
		writeCheckinRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, checkinResp{
		AwardMilli: credits.FormatAmount(res.AwardMilli),
		Credits:    credits.FormatAmount(res.CreditsAfter),
	})
}

// writeCheckinRepoErr maps the repository sentinels to the stable envelope.
// The disabled sentinel carries ONE message regardless of cause, so the
// envelope (status, code, message, source) is byte-identical for timezone
// unset, mode disabled, a level-gated denial and a corrupt configuration.
func writeCheckinRepoErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrCheckinDisabled):
		writeErr(w, httperr.New(httperr.CodeFeatureDisabled, "签到未启用"))
	case errors.Is(err, db.ErrAlreadyCheckedIn):
		writeErr(w, httperr.New(httperr.CodeAlreadyCheckedIn, "今日已签到"))
	case errors.Is(err, db.ErrCheckinCapReached):
		writeErr(w, httperr.New(httperr.CodeCheckinCapReached, "悠哉积分已达签到上限，无法签到"))
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, db.ErrLevelAdminExcluded), errors.Is(err, db.ErrAdminProtected):
		writeErr(w, httperr.New(httperr.CodeForbidden, "forbidden"))
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func writeErr(w http.ResponseWriter, e httperr.Error) {
	httperr.WriteError(w, e)
}
