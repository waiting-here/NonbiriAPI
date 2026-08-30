package resources

import "net/http"

type regenerateCallerKeyRequest struct {
	ExpectedGeneration requestField[string] `json:"expected_generation"`
}

func (api *httpAPI) getCallerKey(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	state, err := api.repository.GetCallerKey(request.Context(), principal.UserID)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writer.Header().Set("X-Nonbiri-CallerKey-Generation", state.Generation)
	if state.Metadata == nil {
		writeJSON(writer, http.StatusOK, nil)
		return
	}
	writeJSON(writer, http.StatusOK, state.Metadata)
}

func (api *httpAPI) regenerateCallerKey(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	var body regenerateCallerKeyRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	generation, ok := canonicalExpectedGeneration(body.ExpectedGeneration)
	if !ok {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	result, err := api.repository.RegenerateCallerKey(request.Context(), principal.UserID, generation)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
