package aiagent

import (
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
	"github.com/waiting-here/NonbiriAPI/internal/codexauth"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// RegisterDriverRoutes registers administrator-only model and connection
// management. The existing single-driver endpoints remain compatible.
func RegisterDriverRoutes(registrar Registrar, service *Service, requests *agentrequest.Runtime, connection *chatgptauth.Manager) error {
	if registrar == nil || service == nil || requests == nil || connection == nil {
		return ErrInvalid
	}
	api := &httpAPI{s: service, requests: requests}
	write := func(w http.ResponseWriter, value any, err error) {
		switch {
		case errors.Is(err, codexauth.ErrInvalid):
			err = ErrInvalid
		case errors.Is(err, codexauth.ErrConflict):
			err = ErrConflict
		case errors.Is(err, codexauth.ErrDisconnected):
			httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "Codex连接已失效，请重新连接。"))
			return
		case errors.Is(err, chatgptauth.ErrInvalid):
			err = ErrInvalid
		case errors.Is(err, chatgptauth.ErrConflict):
			err = ErrConflict
		case errors.Is(err, chatgptauth.ErrDisconnected):
			httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "ChatGPT连接已失效，请重新登录并导入。"))
			return
		}
		var codexError *codexauth.OAuthError
		if errors.As(err, &codexError) {
			httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "Codex登录暂时不可用，请确认账号允许设备码登录后重试。"))
			return
		}
		var oauth *chatgptauth.OAuthError
		if errors.As(err, &oauth) {
			httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "ChatGPT连接暂时不可用，请稍后重试或重新登录。"))
			return
		}
		writeResult(w, value, err)
	}
	register := func(method, path string, handler AuthorizedHandler) error {
		return registrar.RegisterAdminRoute(method, "/admin/api/ai"+path, func(w http.ResponseWriter, r *http.Request, actor Actor) {
			if err := api.admin(r.Context(), actor, "ai.config"); err != nil {
				write(w, nil, err)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			handler(w, r, actor)
		})
	}
	routes := []route{
		{"GET", "/codex", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			v, e := requests.CodexStatus(r.Context())
			write(w, v, e)
		}},
		{"POST", "/codex/login", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			var in struct {
				ExpectedRevision int64 `json:"expected_revision,string"`
			}
			if !readBody(w, r, &in) {
				return
			}
			v, e := requests.BeginCodex(r.Context(), in.ExpectedRevision)
			write(w, v, e)
		}},
		{"POST", "/codex/cancel", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			var in struct {
				LoginID string `json:"login_id"`
			}
			if !readBody(w, r, &in) {
				return
			}
			v, e := requests.CancelCodex(r.Context(), in.LoginID)
			write(w, v, e)
		}},
		{"POST", "/codex/logout", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			var in struct {
				ExpectedRevision int64 `json:"expected_revision,string"`
			}
			if !readBody(w, r, &in) {
				return
			}
			v, e := requests.DisconnectCodex(r.Context(), in.ExpectedRevision)
			write(w, v, e)
		}},
		{"GET", "/drivers", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			v, e := requests.Drivers(r.Context())
			write(w, v, e)
		}},
		{"POST", "/drivers", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			var in agentrequest.DriverUpdate
			if !readBody(w, r, &in) {
				return
			}
			v, e := requests.PutDriver(r.Context(), 0, in)
			write(w, v, e)
		}},
		{"PUT", "/drivers/{id}", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			id, ok := pathID(w, r, "id")
			if !ok {
				return
			}
			var in agentrequest.DriverUpdate
			if !readBody(w, r, &in) {
				return
			}
			v, e := requests.PutDriver(r.Context(), id, in)
			write(w, v, e)
		}},
		{"DELETE", "/drivers/{id}", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			id, ok := pathID(w, r, "id")
			if !ok {
				return
			}
			var in struct {
				ExpectedRevision int64 `json:"expected_revision,string"`
			}
			if !readBody(w, r, &in) {
				return
			}
			write(w, struct{}{}, requests.DeleteDriver(r.Context(), id, in.ExpectedRevision))
		}},
		{"PUT", "/driver-bindings", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			var in agentrequest.BindingUpdate
			if !readBody(w, r, &in) {
				return
			}
			v, e := requests.PutBinding(r.Context(), in)
			write(w, v, e)
		}},
		{"POST", "/drivers/{id}/models", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			id, ok := pathID(w, r, "id")
			if !ok {
				return
			}
			v, e := requests.DiscoverDriverModels(r.Context(), id)
			write(w, map[string]any{"models": v}, e)
		}},
		{"GET", "/chatgpt", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			v, e := requests.ChatGPTStatus(r.Context())
			write(w, v, e)
		}},
		{"POST", "/chatgpt/target", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			var in struct {
				NewAccount bool `json:"new_account"`
			}
			if r.ContentLength != 0 && !readBody(w, r, &in) {
				return
			}
			v, e := connection.TargetFor(r.Context(), in.NewAccount)
			write(w, v, e)
		}},
		{"POST", "/chatgpt/import", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			var in chatgptauth.Envelope
			if !readBody(w, r, &in) {
				return
			}
			v, e := connection.Import(r.Context(), in)
			write(w, v, e)
		}},
		{"POST", "/chatgpt/logout", func(w http.ResponseWriter, r *http.Request, _ Actor) {
			var in struct {
				ExpectedRevision int64 `json:"expected_revision,string"`
			}
			if !readBody(w, r, &in) {
				return
			}
			v, e := requests.DisconnectChatGPT(r.Context(), in.ExpectedRevision)
			write(w, v, e)
		}},
	}
	for _, entry := range routes {
		if err := register(entry.method, entry.path, entry.handler); err != nil {
			return err
		}
	}
	return nil
}
