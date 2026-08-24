package donation

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// ReviewerIdentity is the entire principal a review route receives: an opaque
// user id plus the mounted role ("admin" or "level5"). It deliberately carries
// no session material and no user row.
type ReviewerIdentity struct {
	UserID int64
	Role   string // db.ReviewRoleAdmin | db.ReviewRoleLevel5
}

// ReviewIdentityResolver extracts the reviewer identity from a request.
type ReviewIdentityResolver func(*http.Request) (ReviewerIdentity, error)

// ReviewHandler is the mountable donation-review surface shared by the
// administrator station (/admin/api/donations...) and the level-5 steward
// prefix (/api/steward/donations...). Both frames resolve their own identity;
// every mutation re-validates the role server-side in the repository.
type ReviewHandler struct {
	svc      *Service
	identity ReviewIdentityResolver
	prefix   string
	mux      *http.ServeMux
}

// NewReviewHandler builds the surface for one prefix (e.g. "/admin/api" or
// "/api/steward") with its own mux and identity resolver.
func NewReviewHandler(prefix string, svc *Service, identity ReviewIdentityResolver) *ReviewHandler {
	h := &ReviewHandler{svc: svc, identity: identity, prefix: prefix, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET "+prefix+"/donations", func(w http.ResponseWriter, r *http.Request) { h.serveList(w, r) })
	h.mux.HandleFunc("GET "+prefix+"/donations/{id}", func(w http.ResponseWriter, r *http.Request) { h.serveGet(w, r) })
	h.mux.HandleFunc("PATCH "+prefix+"/donations/{id}", func(w http.ResponseWriter, r *http.Request) { h.serveReview(w, r) })
	h.mux.HandleFunc("DELETE "+prefix+"/donations/{id}", func(w http.ResponseWriter, r *http.Request) { h.serveDelete(w, r) })
	return h
}

// --- externally mountable sub-handlers --------------------------------------
//
// Frames that resolve their own principals (the steward gate hands sub-
// handlers a ready Principal) can wire these directly instead of mounting the
// whole tree. Each sub-handler re-validates the role server-side.

// ListSub serves GET {prefix}/donations for one resolved reviewer.
func (h *ReviewHandler) ListSub(w http.ResponseWriter, r *http.Request, idn ReviewerIdentity) {
	h.listFor(w, r, idn)
}

// GetSub serves GET {prefix}/donations/{id} for one resolved reviewer.
func (h *ReviewHandler) GetSub(w http.ResponseWriter, r *http.Request, idn ReviewerIdentity) {
	h.getFor(w, r, idn)
}

// ReviewSub serves PATCH {prefix}/donations/{id} for one resolved reviewer.
func (h *ReviewHandler) ReviewSub(w http.ResponseWriter, r *http.Request, idn ReviewerIdentity) {
	h.reviewFor(w, r, idn)
}

// DeleteSub serves DELETE {prefix}/donations/{id} for one resolved reviewer.
func (h *ReviewHandler) DeleteSub(w http.ResponseWriter, r *http.Request, idn ReviewerIdentity) {
	h.deleteFor(w, r, idn)
}

func (h *ReviewHandler) resolve(w http.ResponseWriter, r *http.Request) (ReviewerIdentity, bool) {
	if h.identity == nil {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return ReviewerIdentity{}, false
	}
	id, err := h.identity(r)
	if err != nil || id.UserID <= 0 || (id.Role != db.ReviewRoleAdmin && id.Role != db.ReviewRoleLevel5) {
		writeErr(w, httperr.New(httperr.CodeForbidden, "reviewer authorization required"))
		return ReviewerIdentity{}, false
	}
	return id, true
}

func (h *ReviewHandler) serveList(w http.ResponseWriter, r *http.Request) {
	if idn, ok := h.resolve(w, r); ok {
		h.listFor(w, r, idn)
	}
}

func (h *ReviewHandler) serveGet(w http.ResponseWriter, r *http.Request) {
	if idn, ok := h.resolve(w, r); ok {
		h.getFor(w, r, idn)
	}
}

func (h *ReviewHandler) serveReview(w http.ResponseWriter, r *http.Request) {
	if idn, ok := h.resolve(w, r); ok {
		h.reviewFor(w, r, idn)
	}
}

func (h *ReviewHandler) serveDelete(w http.ResponseWriter, r *http.Request) {
	if idn, ok := h.resolve(w, r); ok {
		h.deleteFor(w, r, idn)
	}
}

// list handles GET {prefix}/donations?page=&page_size=&status=.
func (h *ReviewHandler) listFor(w http.ResponseWriter, r *http.Request, _ ReviewerIdentity) {
	page, pageSize, status, ok := parseReviewListParams(w, r)
	if !ok {
		return
	}
	donations, total, err := h.svc.ListForReview(r.Context(), status, pageSize, (page-1)*pageSize)
	if err != nil {
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, listResp{
		Data:    donationsResp(donations),
		Total:   total,
		HasMore: page*pageSize < total,
	})
}

// parseReviewListParams parses the review route's complete allowlist in one
// pass. Missing status means all statuses; an empty value or the literal
// "all" is deliberately rejected so the wire has one unambiguous all-state.
func parseReviewListParams(w http.ResponseWriter, r *http.Request) (page, pageSize int, status string, ok bool) {
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > 8192 {
		writeInvalid(w)
		return 0, 0, "", false
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeInvalid(w)
		return 0, 0, "", false
	}
	for key := range q {
		switch key {
		case "page", "page_size", "status":
		default:
			writeInvalid(w)
			return 0, 0, "", false
		}
	}
	page, pageSize = 1, 20
	parsePositive := func(name string, maximum int) (int, bool) {
		raw, present := q[name]
		if !present {
			return 0, true
		}
		if len(raw) != 1 || raw[0] == "" {
			return 0, false
		}
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || (maximum > 0 && n > maximum) || raw[0] != strconv.Itoa(n) {
			return 0, false
		}
		return n, true
	}
	if parsed, valid := parsePositive("page", 0); !valid {
		writeInvalid(w)
		return 0, 0, "", false
	} else if parsed != 0 {
		page = parsed
	}
	if parsed, valid := parsePositive("page_size", 100); !valid {
		writeInvalid(w)
		return 0, 0, "", false
	} else if parsed != 0 {
		pageSize = parsed
	}
	// Both the repository offset, (page-1)*pageSize, and the response's
	// has-more boundary, page*pageSize, are calculated below. Prove the larger
	// product fits first so neither expression can wrap to a small positive or
	// negative value on this platform.
	if page > math.MaxInt/pageSize {
		writeInvalid(w)
		return 0, 0, "", false
	}
	if raw, present := q["status"]; present {
		if len(raw) != 1 {
			writeInvalid(w)
			return 0, 0, "", false
		}
		switch raw[0] {
		case db.DonationPending, db.DonationApproved, db.DonationRejected, db.DonationDeleted:
			status = raw[0]
		default:
			writeInvalid(w)
			return 0, 0, "", false
		}
	}
	return page, pageSize, status, true
}

// get handles GET {prefix}/donations/{id}: full detail for reviewers.
func (h *ReviewHandler) getFor(w http.ResponseWriter, r *http.Request, _ ReviewerIdentity) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	d, keys, reviews, err := h.svc.GetForReview(r.Context(), id)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, donationResponse(d, keys, reviews))
}

// review handles PATCH {prefix}/donations/{id}: one whole-donation decision.
func (h *ReviewHandler) reviewFor(w http.ResponseWriter, r *http.Request, idn ReviewerIdentity) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	var req reviewRequest
	if derr := decodeJSON(w, r, &req); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	dec := db.ReviewDecision{
		DonationID: id,
		Role:       idn.Role,
		ReviewerID: idn.UserID,
		Action:     req.Action,
		Note:       req.Note,
	}
	switch req.Action {
	case db.ReviewActionApprove, db.ReviewActionReject, db.ReviewActionEnable,
		db.ReviewActionDisable, db.ReviewActionUpdate:
	default:
		writeInvalid(w)
		return
	}
	if req.ExpiresAt != nil {
		expVal, present, ok2 := parseExpiresUpdate(req.ExpiresAt)
		if !ok2 || !present {
			writeInvalid(w)
			return
		}
		dec.ExpiresAt = &expVal
	}
	for _, ku := range req.Keys {
		update := db.DonationKeyUpdate{DonationKeyID: ku.ID}
		if ku.MaxConcurrency != nil {
			if *ku.MaxConcurrency < 0 || int64(*ku.MaxConcurrency) > db.MaxDonationKeyConcurrency {
				writeInvalid(w)
				return
			}
			v := int64(*ku.MaxConcurrency)
			update.MaxConcurrency = &v
		}
		if ku.RPMLimit != nil {
			if *ku.RPMLimit < 0 || int64(*ku.RPMLimit) > db.MaxDonationKeyRPM {
				writeInvalid(w)
				return
			}
			v := int64(*ku.RPMLimit)
			update.RPMLimit = &v
		}
		if ku.CreditsUsageCapMilli != nil {
			v, perr := credits.ParseAmount(*ku.CreditsUsageCapMilli)
			if perr != nil || v < 0 {
				writeInvalid(w)
				return
			}
			update.CreditsUsageCap = &v
		}
		if ku.Enabled != nil {
			update.Enabled = ku.Enabled
		}
		dec.KeyUpdates = append(dec.KeyUpdates, update)
	}

	d, err := h.svc.Review(r.Context(), dec)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, donationResponse(d, nil, nil))
}

// delete handles DELETE {prefix}/donations/{id}: soft delete by a reviewer.
func (h *ReviewHandler) deleteFor(w http.ResponseWriter, r *http.Request, idn ReviewerIdentity) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		writeInvalid(w)
		return
	}
	if err := h.svc.DeleteAsReviewer(r.Context(), id, idn.Role, idn.UserID); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// --- wire shapes ------------------------------------------------------------

type reviewRequest struct {
	Action    string          `json:"action"`
	Note      string          `json:"note,omitempty"`
	ExpiresAt json.RawMessage `json:"expires_at,omitempty"`
	Keys      []reviewKeyReq  `json:"keys,omitempty"`
}

type reviewKeyReq struct {
	ID                   int64   `json:"id"`
	MaxConcurrency       *int    `json:"max_concurrency,omitempty"`
	RPMLimit             *int    `json:"rpm_limit,omitempty"`
	CreditsUsageCapMilli *string `json:"credits_usage_cap_milli,omitempty"`
	Enabled              *bool   `json:"enabled,omitempty"`
}

// Handler returns the raw route tree for external mounting (the caller wraps
// it in httpmw.API and its own authentication middleware).
func (h *ReviewHandler) Handler() http.Handler { return h.mux }
