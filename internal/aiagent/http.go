package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

type AuthorizedHandler func(http.ResponseWriter, *http.Request, Actor)
type Registrar interface {
	RegisterAdminRoute(string, string, AuthorizedHandler) error
	RegisterStewardRoute(string, string, AuthorizedHandler) error
}

type RequestAdministration interface {
	Settings(context.Context) (agentrequest.Settings, error)
	PutSettings(context.Context, agentrequest.SettingsUpdate) (agentrequest.Settings, error)
	DiscoverModels(context.Context) ([]string, error)
	List(context.Context, agentrequest.ListFilter) (agentrequest.Page, error)
	Detail(context.Context, int64) (agentrequest.Detail, error)
	Cleanup(context.Context, agentrequest.CleanupInput) (agentrequest.CleanupResult, error)
	Usage(context.Context) (agentrequest.UsageReport, error)
}

type route struct {
	method, path string
	handler      AuthorizedHandler
}
type httpAPI struct {
	s        *Service
	requests RequestAdministration
}

func RegisterRoutes(registrar Registrar, service *Service, requests RequestAdministration) error {
	if registrar == nil || service == nil || requests == nil {
		return ErrInvalid
	}
	api := &httpAPI{service, requests}
	shared := []route{
		{"GET", "/chat", api.chat}, {"POST", "/chat/messages", api.send}, {"GET", "/chat/messages", api.messages},
		{"PUT", "/chat/messages/{id}", api.edit}, {"POST", "/chat/clear", api.clear}, {"POST", "/chat/jobs/{id}/cancel", api.cancel},
		{"GET", "/chat/plans", api.plans}, {"POST", "/chat/plans/{id}/decision", api.decision}, {"GET", "/reviews", api.reviews}, {"GET", "/reviews/{id}", api.review},
		{"GET", "/memories/{kind}", api.memory}, {"PUT", "/memories/{kind}", api.putMemory},
		{"GET", "/personal-memory", api.personalMemory}, {"PUT", "/personal-memory", api.putPersonalMemory},
	}
	for _, r := range shared {
		if err := registrar.RegisterAdminRoute(r.method, "/admin/api/ai"+r.path, r.handler); err != nil {
			return err
		}
		if err := registrar.RegisterStewardRoute(r.method, "/api/steward/ai"+r.path, r.handler); err != nil {
			return err
		}
	}
	admin := []route{{"GET", "/config", api.settings}, {"PUT", "/config", api.putSettings}, {"GET", "/driver", api.driver}, {"PUT", "/driver", api.putDriver}, {"POST", "/driver/models", api.models}, {"GET", "/requests", api.requestsPage}, {"GET", "/requests/{id}", api.requestDetail}, {"POST", "/requests/cleanup", api.cleanup}, {"GET", "/usage", api.usage}, {"GET", "/archives", api.archives}, {"GET", "/archives/{owner}/messages", api.archiveMessages}, {"GET", "/archives/{owner}/results", api.archiveResults}}
	for _, r := range admin {
		if err := registrar.RegisterAdminRoute(r.method, "/admin/api/ai"+r.path, r.handler); err != nil {
			return err
		}
	}
	for _, r := range []route{{"GET", "/personal-memories", api.memoryOwners}, {"GET", "/personal-memories/{owner}", api.personalMemory}, {"PUT", "/personal-memories/{owner}", api.putPersonalMemory}} {
		if err := registrar.RegisterAdminRoute(r.method, "/admin/api/ai"+r.path, r.handler); err != nil {
			return err
		}
	}
	if err := registrar.RegisterAdminRoute("GET", "/admin/api/ai/archives/{owner}/plans", api.archivePlans); err != nil {
		return err
	}
	return nil
}

func writeResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(w, http.StatusOK, value)
}
func writeError(w http.ResponseWriter, err error) {
	if writeDriverFailure(w, err) {
		return
	}
	code, message := httperr.CodeInternal, "操作未完成，请重试。"
	switch {
	case errors.Is(err, authz.ErrElevatedRequired):
		code, message = httperr.CodeElevationRequired, "请完成身份确认后执行此计划。"
	case errors.Is(err, authz.ErrMFARequired):
		code, message = httperr.CodeMFARequired, "请先完成验证。"
	case errors.Is(err, authz.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "登录已过期，请重新登录。"
	case errors.Is(err, authz.ErrPermissionDenied):
		code, message = httperr.CodeStewardPermissionDenied, "尚无此项权限，可联系管理员或6级协管调整授权。"
	case errors.Is(err, ErrForbidden), errors.Is(err, authz.ErrForbidden):
		code, message = httperr.CodeForbidden, "尚无此项权限。"
	case errors.Is(err, ErrInvalid), errors.Is(err, agentrequest.ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "请检查填写的内容。"
	case errors.Is(err, ErrNotFound), errors.Is(err, agentrequest.ErrNotFound):
		code, message = httperr.CodeNotFound, "记录不存在或当前不可查看。"
	case errors.Is(err, ErrConflict), errors.Is(err, agentrequest.ErrConflict):
		code, message = httperr.CodeConflict, "内容或状态已经变化，请刷新后重试。"
	case errors.Is(err, agentrequest.ErrUnconfigured):
		code, message = httperr.CodeServiceUnavailable, "AI模型尚未配置，请联系管理员。"
	case errors.Is(err, ErrChatDisabled):
		code, message = httperr.CodeServiceUnavailable, "AI对话尚未开放，请联系管理员开启对话并配置驱动模型。"
	case errors.Is(err, ErrUnavailable), errors.Is(err, agentrequest.ErrClosed):
		code, message = httperr.CodeServiceUnavailable, "AI暂时不可用，请稍后重试。"
	}
	httperr.WriteError(w, httperr.New(code, message))
}
func readBody(w http.ResponseWriter, r *http.Request, value any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err == nil {
		err = decodeClosed(raw, value)
	}
	if err != nil {
		writeError(w, ErrInvalid)
		return false
	}
	return true
}
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || v <= 0 {
		writeError(w, ErrInvalid)
		return 0, false
	}
	return v, true
}
func pageArgs(r *http.Request) (int, int, error) {
	page, size := 1, 20
	var err error
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, ErrInvalid
		}
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		size, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, ErrInvalid
		}
	}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return 0, 0, ErrInvalid
	}
	return page, size, nil
}
func (a *httpAPI) admin(ctx context.Context, actor Actor, permission string) error {
	tx, err := a.s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return a.s.authorize(ctx, tx, actor, permission)
}

func (a *httpAPI) chat(w http.ResponseWriter, r *http.Request, actor Actor) {
	v, e := a.s.Conversation(r.Context(), actor)
	writeResult(w, v, e)
}
func (a *httpAPI) send(w http.ResponseWriter, r *http.Request, actor Actor) {
	var input SendInput
	if !readBody(w, r, &input) {
		return
	}
	v, e := a.s.Send(r.Context(), actor, input)
	writeResult(w, v, e)
}
func (a *httpAPI) edit(w http.ResponseWriter, r *http.Request, actor Actor) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var input SendInput
	if !readBody(w, r, &input) {
		return
	}
	v, e := a.s.Edit(r.Context(), actor, id, input)
	writeResult(w, v, e)
}
func (a *httpAPI) clear(w http.ResponseWriter, r *http.Request, actor Actor) {
	var input struct {
		ExpectedGeneration int64 `json:"expected_generation,string"`
	}
	if !readBody(w, r, &input) {
		return
	}
	writeResult(w, struct{}{}, a.s.Clear(r.Context(), actor, input.ExpectedGeneration))
}
func (a *httpAPI) cancel(w http.ResponseWriter, r *http.Request, actor Actor) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	writeResult(w, struct{}{}, a.s.Cancel(r.Context(), actor, id))
}
func (a *httpAPI) decision(w http.ResponseWriter, r *http.Request, actor Actor) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var input struct {
		Approve        *bool  `json:"approve"`
		Feedback       string `json:"feedback"`
		ElevationToken string `json:"elevation_token,omitempty"`
	}
	if !readBody(w, r, &input) {
		return
	}
	if input.Approve == nil {
		writeError(w, ErrInvalid)
		return
	}
	if len(input.ElevationToken) > 4096 {
		writeError(w, ErrInvalid)
		return
	}
	if actor.Role == "admin" {
		actor.Elevation = input.ElevationToken
	}
	v, e := a.s.Decide(r.Context(), actor, id, *input.Approve, input.Feedback)
	writeResult(w, v, e)
}
func (a *httpAPI) messages(w http.ResponseWriter, r *http.Request, actor Actor) {
	a.messagePage(w, r, actor, actor.ID, false)
}
func (a *httpAPI) messagePage(w http.ResponseWriter, r *http.Request, actor Actor, owner int64, archive bool) {
	var before int64
	var err error
	if raw := r.URL.Query().Get("before_id"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			writeError(w, ErrInvalid)
			return
		}
	}
	v, e := a.s.Messages(r.Context(), actor, owner, before, archive)
	writeResult(w, map[string]any{"items": v}, e)
}
func (a *httpAPI) reviews(w http.ResponseWriter, r *http.Request, actor Actor) {
	p, n, e := pageArgs(r)
	if e != nil {
		writeError(w, e)
		return
	}
	v, e := a.s.Reviews(r.Context(), actor, p, n)
	writeResult(w, v, e)
}
func (a *httpAPI) review(w http.ResponseWriter, r *http.Request, actor Actor) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	v, e := a.s.Review(r.Context(), actor, id)
	writeResult(w, v, e)
}
func (a *httpAPI) memory(w http.ResponseWriter, r *http.Request, actor Actor) {
	v, e := a.s.Memory(r.Context(), actor, r.PathValue("kind"))
	writeResult(w, v, e)
}
func (a *httpAPI) putMemory(w http.ResponseWriter, r *http.Request, actor Actor) {
	var input struct {
		Content          string `json:"content"`
		ExpectedRevision int64  `json:"expected_revision,string"`
	}
	if !readBody(w, r, &input) {
		return
	}
	v, e := a.s.PutMemory(r.Context(), actor, r.PathValue("kind"), input.Content, input.ExpectedRevision)
	writeResult(w, v, e)
}
func (a *httpAPI) settings(w http.ResponseWriter, r *http.Request, actor Actor) {
	v, e := a.s.Settings(r.Context(), actor)
	writeResult(w, v, e)
}
func (a *httpAPI) putSettings(w http.ResponseWriter, r *http.Request, actor Actor) {
	var input SettingsUpdate
	if !readBody(w, r, &input) {
		return
	}
	v, e := a.s.PutSettings(r.Context(), actor, input)
	writeResult(w, v, e)
}
func (a *httpAPI) driver(w http.ResponseWriter, r *http.Request, actor Actor) {
	if e := a.admin(r.Context(), actor, "ai.config"); e != nil {
		writeError(w, e)
		return
	}
	v, e := a.requests.Settings(r.Context())
	writeResult(w, v, e)
}
func (a *httpAPI) putDriver(w http.ResponseWriter, r *http.Request, actor Actor) {
	var input agentrequest.SettingsUpdate
	if !readBody(w, r, &input) {
		return
	}
	if e := a.admin(r.Context(), actor, "ai.config"); e != nil {
		writeError(w, e)
		return
	}
	v, e := a.requests.PutSettings(r.Context(), input)
	writeResult(w, v, e)
}
func (a *httpAPI) models(w http.ResponseWriter, r *http.Request, actor Actor) {
	if e := a.admin(r.Context(), actor, "ai.config"); e != nil {
		writeError(w, e)
		return
	}
	v, e := a.requests.DiscoverModels(r.Context())
	writeResult(w, map[string]any{"models": v}, e)
}
func (a *httpAPI) requestsPage(w http.ResponseWriter, r *http.Request, actor Actor) {
	if e := a.admin(r.Context(), actor, "ai.logs"); e != nil {
		writeError(w, e)
		return
	}
	p, n, e := pageArgs(r)
	if e != nil {
		writeError(w, e)
		return
	}
	q := r.URL.Query()
	filter := agentrequest.ListFilter{Provider: q.Get("provider"), Source: q.Get("source"), Kind: q.Get("kind"), State: q.Get("state"), Page: p, PageSize: n}
	if raw := q.Get("actor_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 0 {
			writeError(w, ErrInvalid)
			return
		}
		filter.ActorID = &id
	}
	for key, target := range map[string]*int64{"from": &filter.From, "to": &filter.To} {
		if raw := q.Get(key); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 0 {
				writeError(w, ErrInvalid)
				return
			}
			*target = value
		}
	}
	v, e := a.requests.List(r.Context(), filter)
	writeResult(w, v, e)
}
func (a *httpAPI) requestDetail(w http.ResponseWriter, r *http.Request, actor Actor) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if e := a.admin(r.Context(), actor, "ai.logs"); e != nil {
		writeError(w, e)
		return
	}
	v, e := a.requests.Detail(r.Context(), id)
	writeResult(w, v, e)
}
func (a *httpAPI) cleanup(w http.ResponseWriter, r *http.Request, actor Actor) {
	var input agentrequest.CleanupInput
	if !readBody(w, r, &input) {
		return
	}
	if e := a.admin(r.Context(), actor, "ai.logs"); e != nil {
		writeError(w, e)
		return
	}
	v, e := a.requests.Cleanup(r.Context(), input)
	writeResult(w, v, e)
}
func (a *httpAPI) usage(w http.ResponseWriter, r *http.Request, actor Actor) {
	if e := a.admin(r.Context(), actor, "ai.logs"); e != nil {
		writeError(w, e)
		return
	}
	v, e := a.requests.Usage(r.Context())
	writeResult(w, v, e)
}

func (a *httpAPI) archives(w http.ResponseWriter, r *http.Request, actor Actor) {
	p, n, err := pageArgs(r)
	if err != nil {
		writeError(w, err)
		return
	}
	tx, err := a.s.read.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.s.authorize(r.Context(), tx, actor, "ai.archives"); err != nil {
		writeError(w, err)
		return
	}
	type entry struct {
		ActorID   int64  `json:"actor_id,string"`
		Role      string `json:"role"`
		Label     string `json:"label"`
		UpdatedAt int64  `json:"updated_at"`
	}
	out := Page[entry]{Items: []entry{}, Page: p, PageSize: n}
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM ai_agent_conversations`).Scan(&out.Total); err != nil {
		writeError(w, err)
		return
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT actor_id,actor_role,actor_label,updated_at FROM ai_agent_conversations ORDER BY updated_at DESC,id DESC LIMIT ? OFFSET ?`, n, (p-1)*n)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var v entry
		if err = rows.Scan(&v.ActorID, &v.Role, &v.Label, &v.UpdatedAt); err != nil {
			writeError(w, err)
			return
		}
		out.Items = append(out.Items, v)
	}
	writeResult(w, out, rows.Err())
}
func (a *httpAPI) archiveMessages(w http.ResponseWriter, r *http.Request, actor Actor) {
	owner, ok := pathID(w, r, "owner")
	if ok {
		a.messagePage(w, r, actor, owner, true)
	}
}
func (a *httpAPI) archiveResults(w http.ResponseWriter, r *http.Request, actor Actor) {
	owner, ok := pathID(w, r, "owner")
	if !ok {
		return
	}
	p, n, err := pageArgs(r)
	if err != nil {
		writeError(w, err)
		return
	}
	tx, err := a.s.read.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.s.authorize(r.Context(), tx, actor, "ai.archives"); err != nil {
		writeError(w, err)
		return
	}
	out := Page[ModelResult]{Items: []ModelResult{}, Page: p, PageSize: n}
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM ai_agent_model_results r JOIN ai_agent_jobs j ON j.id=r.job_id WHERE j.actor_id=? AND j.kind<>'auto_review'`, owner).Scan(&out.Total); err != nil {
		writeError(w, err)
		return
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT r.request_id,r.kind,r.message,r.error,r.created_at FROM ai_agent_model_results r JOIN ai_agent_jobs j ON j.id=r.job_id WHERE j.actor_id=? AND j.kind<>'auto_review' ORDER BY r.request_id DESC LIMIT ? OFFSET ?`, owner, n, (p-1)*n)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var v ModelResult
		var raw string
		if err = rows.Scan(&v.RequestID, &v.Kind, &raw, &v.Error, &v.CreatedAt); err != nil {
			writeError(w, err)
			return
		}
		v.Message = json.RawMessage(raw)
		out.Items = append(out.Items, v)
	}
	writeResult(w, out, rows.Err())
}
