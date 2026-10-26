package donation

import "net/http"

const (
	routeAdminUserDonations   = "/admin/api/users/{userId}/donations"
	routeStewardUserDonations = "/api/steward/users/{userId}/donations"
)

func (api *httpAPI) userHistoryHandler(role reviewerRole, detail bool) AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		targetID, ok := parsePathID(w, r, "userId")
		if !ok || !requireNoBody(w, r) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if detail {
			donationID, ok := parsePathID(w, r, "id")
			if !ok || !requireEmptyQuery(w, r) {
				return
			}
			out, err := api.service.UserDonationHistoryDetail(r.Context(), role, p.UserID, targetID, donationID)
			if err != nil {
				writeDonationError(w, err)
			} else {
				writeJSON(w, out)
			}
			return
		}
		values, ok := requestQuery(w, r)
		if !ok {
			return
		}
		page, err := parseNumberedQuery(values, "status", "review_status", "q")
		if err != nil {
			writeDonationError(w, err)
			return
		}
		filter := ManagementFilter{Status: values.Get("status"), ReviewStatus: values.Get("review_status"), Query: values.Get("q")}
		out, err := api.service.UserDonationHistoryPage(r.Context(), role, p.UserID, targetID, filter, page)
		if err != nil {
			writeDonationError(w, err)
		} else {
			writeJSON(w, out)
		}
	}
}
