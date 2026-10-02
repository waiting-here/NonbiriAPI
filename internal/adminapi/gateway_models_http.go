package adminapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const (
	RouteAdminGatewayModels = "/admin/api/gateway-model-capabilities"
	RouteAdminGatewayModel  = "/admin/api/gateway-model-capabilities/{id}"
)

func (runtime *SiteConfigRuntime) getGatewayModels(w http.ResponseWriter, r *http.Request, p SiteConfigAdminPrincipal) {
	if !requireSiteConfigMethod(w, r, http.MethodGet) || !requireSiteConfigReadRequest(w, r) {
		return
	}
	result, err := runtime.repository.ReadGatewayModels(r.Context(), p.UserID)
	if err != nil {
		writeSiteConfigError(w, err)
		return
	}
	writeSiteConfigJSON(w, http.StatusOK, result)
}
func (runtime *SiteConfigRuntime) mutateGatewayModel(w http.ResponseWriter, r *http.Request, p SiteConfigAdminPrincipal) {
	if !requireSiteConfigNoQuery(w, r) {
		return
	}
	key, ok := requireSiteConfigIdempotencyKey(w, r)
	if !ok {
		return
	}
	var id int64
	if r.Method != http.MethodPost {
		raw := r.PathValue("id")
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != raw {
			writeSiteConfigError(w, ErrSiteConfigInvalid)
			return
		}
		id = parsed
	}
	object, ok := decodeSiteConfigObject(w, r)
	if !ok {
		return
	}
	raw, err := json.Marshal(object)
	if err != nil {
		writeSiteConfigError(w, ErrSiteConfigInvalid)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input GatewayModelInput
	if decoder.Decode(&input) != nil {
		writeSiteConfigError(w, ErrSiteConfigInvalid)
		return
	}
	result, err := runtime.repository.MutateGatewayModel(r.Context(), p.UserID, id, r.Method, key, input)
	if err != nil {
		if field := gatewayValidationField(err); field != "" {
			httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "Check the "+field+" field."))
		} else {
			writeSiteConfigError(w, err)
		}
		return
	}
	writeSiteConfigMutation(w, result)
}
