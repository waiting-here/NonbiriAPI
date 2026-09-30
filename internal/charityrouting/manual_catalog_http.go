package charityrouting

import (
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"net/http"
)

func (api *httpAPI) adminAddManual(w http.ResponseWriter, r *http.Request) {
	api.manualCatalog(w, r, true, 0, false)
}
func (api *httpAPI) adminDeleteManual(w http.ResponseWriter, r *http.Request) {
	api.manualCatalog(w, r, true, 0, true)
}
func (api *httpAPI) stewardAddManual(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.manualCatalog(w, r, false, p.UserID, false)
}
func (api *httpAPI) stewardDeleteManual(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.manualCatalog(w, r, false, p.UserID, true)
}

func (api *httpAPI) manualCatalog(w http.ResponseWriter, r *http.Request, admin bool, actorID int64, deleting bool) {
	selected, err := charityscope.SelectRequest(r)
	if err != nil {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	r = selected
	if !requireEmptyQuery(w, r) {
		return
	}
	donationID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(w, r, "keyId")
	if !ok {
		return
	}
	var entryID int64
	if deleting {
		entryID, ok = parsePathID(w, r, "entryId")
		if !ok {
			return
		}
	}
	var input ManualCatalogInput
	var canonical any
	if deleting {
		var wire struct {
			Expected requiredField[string] `json:"expected_manual_catalog_revision"`
		}
		if !decodeStrictObject(w, r, &wire) {
			return
		}
		if !wire.Expected.Set {
			writeRoutingError(w, ErrInvalidRequest)
			return
		}
		input.ExpectedManualCatalogRevision = wire.Expected.Value
		canonical = map[string]any{"expected_manual_catalog_revision": wire.Expected.Value}
	} else {
		var wire struct {
			Entries  requiredField[[]string] `json:"entries"`
			Expected requiredField[string]   `json:"expected_manual_catalog_revision"`
		}
		if !decodeStrictObject(w, r, &wire) {
			return
		}
		if !wire.Expected.Set || !wire.Entries.Set {
			writeRoutingError(w, ErrInvalidRequest)
			return
		}
		input = ManualCatalogInput{Entries: wire.Entries.Value, ExpectedManualCatalogRevision: wire.Expected.Value}
		canonical = map[string]any{"entries": wire.Entries.Value, "expected_manual_catalog_revision": wire.Expected.Value}
	}
	route := routeStewardManual
	if admin {
		route = routeAdminManual
	}
	ids := []int64{donationID, keyID}
	if deleting {
		route += "/{entryId}"
		ids = append(ids, entryID)
	}
	mutation, ok := mutationFor(w, r, route, ids, canonical)
	if !ok {
		return
	}
	mutation.Query = charityscope.Query(r.Context())
	out, err := api.service.MutateManualCatalog(r.Context(), admin, actorID, donationID, keyID, entryID, mutation, input)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeMutation(w, out)
}
