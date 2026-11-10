package donation

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

// Path overrides use the established session-only management route boundary.
func (api *httpAPI) useRerankPathHandler(role reviewerRole) AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		if _, caller := authz.StewardCallerFromContext(r.Context()); caller {
			writeDonationError(w, ErrForbidden)
			return
		}
		donationID, ok := parsePathID(w, r, "id")
		if !ok {
			return
		}
		useID, ok := parsePathID(w, r, "useId")
		if !ok || !requireEmptyQuery(w, r) {
			return
		}
		var wire struct {
			ExpectedRevision   requiredField[string] `json:"expected_revision"`
			RerankPathOverride nullableField[string] `json:"rerank_path_override"`
		}
		if !decodeStrictObject(w, r, &wire) {
			return
		}
		revision, err := requiredRevision(wire.ExpectedRevision)
		if err != nil || !wire.RerankPathOverride.Set {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
		managementlog.SetAction(r.Context(), "rerank_path.update", "修改重排接口")
		canonical := map[string]any{"expected_revision": wire.ExpectedRevision.Value, "rerank_path_override": wire.RerankPathOverride.Value}
		mutation, ok := mutationFor(w, r, useRoute(role)+"/{useId}", []int64{donationID, useID}, canonical)
		if !ok {
			return
		}
		result, err := api.service.mutateUse(r.Context(), role, p.UserID, donationID, useID, UsePatch{ExpectedRevision: revision, RerankPathOverride: &wire.RerankPathOverride.Value}, "rerank_path", mutation)
		if err != nil {
			writeDonationError(w, err)
			return
		}
		writeMutation(w, result)
	}
}
