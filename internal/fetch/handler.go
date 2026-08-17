package fetch

import (
	"errors"
	"net/http"
	"strconv"

	"nonbiriapi/internal/db"
	"nonbiriapi/internal/httperr"
)

// IdentityResolver extracts the authenticated user id from a request. The
// production resolver is wired by the identity rail (H) from its session
// context; tests inject a fixed id. A request without a valid identity yields
// an error that the handler maps to unauthorized.
type IdentityResolver func(*http.Request) (int64, error)

// HandlerDeps are the collaborators the fetch handler needs.
type HandlerDeps struct {
	Fetcher  *Fetcher
	Store    *db.Store
	Identity IdentityResolver
}

// Handler is the mountable user-station API for the fetched-model routes:
//
//	GET  /api/endpoints/{id}/keys/{keyId}/models
//	POST /api/endpoints/{id}/keys/{keyId}/models/refresh
//
// It does not mount itself; NewHandler returns an http.Handler for the
// integration rail to register under those two patterns. Cross-user, missing,
// or wrong-endpoint combos are indistinguishable (not_found), and a refresh
// never blocks on upstream work (202 once the fetch is queued).
type Handler struct {
	fetcher  *Fetcher
	store    *db.Store
	identity IdentityResolver
	mux      *http.ServeMux
}

// NewHandler builds the route tree. A nil Identity denies every request
// (unauthorized), which keeps the boundary safe until the identity rail wires
// its resolver.
func NewHandler(deps HandlerDeps) http.Handler {
	h := &Handler{
		fetcher:  deps.Fetcher,
		store:    deps.Store,
		identity: deps.Identity,
		mux:      http.NewServeMux(),
	}
	h.mux.HandleFunc("GET /api/endpoints/{id}/keys/{keyId}/models", h.listModels)
	h.mux.HandleFunc("POST /api/endpoints/{id}/keys/{keyId}/models/refresh", h.refreshModels)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// authenticate resolves the caller's user id. A missing identity resolver, a
// resolver error, or a non-positive id all yield unauthorized.
func (h *Handler) authenticate(r *http.Request) (int64, bool) {
	if h.identity == nil {
		return 0, false
	}
	uid, err := h.identity(r)
	if err != nil || uid <= 0 {
		return 0, false
	}
	return uid, true
}

// listModels returns the cached upstream models for the (Endpoint, Key)
// combo. Metadata only: upstream model ids and providers are untrusted text
// rendered as text by the frontend. Cross-user / missing / wrong-endpoint
// combos yield not_found; an owned combo with no cache rows yields an empty
// list.
func (h *Handler) listModels(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		writeFetchErr(w, httperr.New(httperr.CodeServiceUnavailable, "model fetch service is unavailable"))
		return
	}
	uid, ok := h.authenticate(r)
	if !ok {
		writeFetchErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	endpointID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, r, "keyId")
	if !ok {
		return
	}
	models, err := h.store.ListFetchedModels(r.Context(), uid, endpointID, keyID)
	if err != nil {
		writeFetchServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, fetchedModelListResponse(models))
}

// refreshModels triggers one manual upstream fetch for the combo. Only an
// enabled endpoint/key owned by the caller is accepted; anything else is
// indistinguishable not_found. The response is 202 as soon as the fetch is
// queued (or merged into an already-pending fetch); upstream work happens on
// the bounded pool and its outcome is never echoed back. Frequency and queue
// bounds surface as rate_limited.
func (h *Handler) refreshModels(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil || h.fetcher == nil {
		writeFetchErr(w, httperr.New(httperr.CodeServiceUnavailable, "model fetch service is unavailable"))
		return
	}
	uid, ok := h.authenticate(r)
	if !ok {
		writeFetchErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	endpointID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, r, "keyId")
	if !ok {
		return
	}

	// Ownership + enabled gate before any queueing: a cross-user or disabled
	// combo is rejected up front (not_found, indistinguishable from missing)
	// and never triggers upstream work.
	state, err := h.store.GetEndpointKeyFetchState(r.Context(), uid, endpointID, keyID)
	if err != nil {
		writeFetchServiceErr(w, err)
		return
	}
	if !state.EndpointEnabled || !state.KeyEnabled {
		writeFetchErr(w, httperr.New(httperr.CodeNotFound, "not found"))
		return
	}

	if err := h.fetcher.RefreshManual(r.Context(), uid, endpointID, keyID); err != nil {
		switch {
		case errors.Is(err, ErrRefreshRateLimited), errors.Is(err, ErrPoolBusy):
			writeFetchErr(w, httperr.New(httperr.CodeRateLimited, "too many model refresh requests"))
		case errors.Is(err, ErrPoolClosed):
			writeFetchErr(w, httperr.New(httperr.CodeServiceUnavailable, "model fetch service is unavailable"))
		default:
			writeFetchErr(w, httperr.New(httperr.CodeInternal, "internal error"))
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
}

// --- response DTOs ---------------------------------------------------------

type fetchedModelResp struct {
	UpstreamModelID string `json:"upstream_model_id"`
	Provider        string `json:"provider"`
	FetchedAt       int64  `json:"fetched_at"`
	Status          string `json:"status"`
}

func fetchedModelResponse(m db.FetchedModel) fetchedModelResp {
	return fetchedModelResp{
		UpstreamModelID: m.UpstreamModelID,
		Provider:        m.Provider,
		FetchedAt:       m.FetchedAt,
		Status:          m.Status,
	}
}

func fetchedModelListResponse(models []db.FetchedModel) []fetchedModelResp {
	out := make([]fetchedModelResp, 0, len(models))
	for _, m := range models {
		out = append(out, fetchedModelResponse(m))
	}
	return out
}

// --- helpers ---------------------------------------------------------------

func parsePathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeFetchErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid id"))
		return 0, false
	}
	return id, true
}

// writeFetchServiceErr maps a repository error to the stable envelope.
func writeFetchServiceErr(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNotFound) {
		writeFetchErr(w, httperr.New(httperr.CodeNotFound, "not found"))
		return
	}
	writeFetchErr(w, httperr.New(httperr.CodeInternal, "internal error"))
}

func writeFetchErr(w http.ResponseWriter, e httperr.Error) {
	httperr.WriteError(w, e)
}
