package aiagent

import (
	"net/http"
)

func memoryOwnerID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if r.PathValue("owner") == "" {
		return 0, true
	}
	return pathID(w, r, "owner")
}

func (a *httpAPI) personalMemory(w http.ResponseWriter, r *http.Request, actor Actor) {
	owner, ok := memoryOwnerID(w, r)
	if !ok {
		return
	}
	value, err := a.s.PersonalMemory(r.Context(), actor, owner)
	writeResult(w, value, err)
}

func (a *httpAPI) putPersonalMemory(w http.ResponseWriter, r *http.Request, actor Actor) {
	owner, ok := memoryOwnerID(w, r)
	if !ok {
		return
	}
	var input struct {
		Content          string `json:"content"`
		ExpectedRevision int64  `json:"expected_revision,string"`
	}
	if !readBody(w, r, &input) {
		return
	}
	value, err := a.s.PutPersonalMemory(r.Context(), actor, owner, input.Content, input.ExpectedRevision)
	writeResult(w, value, err)
}

func (a *httpAPI) memoryOwners(w http.ResponseWriter, r *http.Request, actor Actor) {
	page, size, err := pageArgs(r)
	if err != nil {
		writeError(w, err)
		return
	}
	value, err := a.s.MemoryOwners(r.Context(), actor, r.URL.Query().Get("q"), page, size)
	writeResult(w, value, err)
}
