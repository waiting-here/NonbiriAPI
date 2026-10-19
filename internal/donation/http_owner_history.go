package donation

import "net/http"

func (api *httpAPI) setOwnerHistory(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	id, ok := parsePathID(w, r, "id")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	var body struct {
		Hidden requiredField[bool] `json:"hidden"`
	}
	if !decodeStrictObject(w, r, &body) {
		return
	}
	if !body.Hidden.Set {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	mutation, ok := mutationFor(w, r, routeOwnerHistory, []int64{id}, map[string]any{"hidden": body.Hidden.Value})
	if !ok {
		return
	}
	result, err := api.service.SetOwnerHistory(r.Context(), p.UserID, id, body.Hidden.Value, mutation)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeMutation(w, result)
}
func (api *httpAPI) ownerHistoryBatch(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	if !requireEmptyQuery(w, r) {
		return
	}
	var body struct {
		Action requiredField[string] `json:"action"`
		Status nullableField[string] `json:"status"`
		Query  requiredField[string] `json:"q"`
		Cursor nullableField[string] `json:"cursor"`
	}
	if !decodeStrictObject(w, r, &body) {
		return
	}
	if !body.Action.Set || !body.Status.Set || !body.Query.Set || !body.Cursor.Set {
		writeDonationError(w, ErrInvalidRequest)
		return
	}
	input := OwnerHistorySelection{Action: body.Action.Value, Query: body.Query.Value}
	if body.Status.Value != nil {
		input.Status = *body.Status.Value
		if input.Status == "" {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
	}
	if body.Cursor.Value != nil {
		input.Cursor = *body.Cursor.Value
		if input.Cursor == "" {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
	}
	mutation, ok := mutationFor(w, r, routeOwnerHistoryBatch, nil, map[string]any{"action": input.Action, "status": body.Status.Value, "q": input.Query, "cursor": body.Cursor.Value})
	if !ok {
		return
	}
	result, err := api.service.OwnerHistoryBatch(r.Context(), p.UserID, input, mutation)
	if err != nil {
		writeDonationError(w, err)
		return
	}
	writeMutation(w, result)
}
