package duel

import (
	"encoding/json"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) registerAIRoutes(user resources.UserRouteRegistrar) error {
	if s.aiAdapter == nil {
		return nil
	}
	base := "/api/games/" + s.rules.ID() + "/ai"
	if err := user.RegisterUserRoute("GET", base, func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
		if r.URL.RawQuery != "" {
			writeError(w, ErrInvalidRequest)
			return
		}
		if !noBody(w, r) {
			return
		}
		value, err := s.ReadAI(r.Context(), Identity{UserID: p.UserID})
		writeValue(w, value, err)
	}); err != nil {
		return err
	}
	if provider, ok := s.aiAdapter.(interface{ UsesMemory() bool }); ok && !provider.UsesMemory() {
		return nil
	}
	return user.RegisterUserRoute("POST", base+"/preference", func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
		if r.URL.RawQuery != "" {
			writeError(w, ErrInvalidRequest)
			return
		}
		key, ok := requestKey(w, r)
		if !ok {
			return
		}
		var body AIPreference
		if !readJSON(w, r, &body) {
			return
		}
		value, err := s.SetAIPreference(r.Context(), Identity{UserID: p.UserID}, key, body)
		writeMutation(w, value, err)
	})
}

func (s *Service) registerAIAdminRoutes(admin host.AdminRegistrar) error {
	if s.aiAdapter == nil {
		return nil
	}
	base := "/admin/api/games/" + s.rules.ID() + "/ai"
	if err := admin.RegisterAdminRoute("GET", base, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			writeError(w, ErrInvalidRequest)
			return
		}
		if !noBody(w, r) {
			return
		}
		value, err := s.AdminAI(r.Context())
		writeAdminValue(w, value, err)
	})); err != nil {
		return err
	}
	names := []string{"settings", "policies", "bots", "preview"}
	if provider, ok := s.aiAdapter.(interface{ UsesMemory() bool }); ok && !provider.UsesMemory() {
		names = names[:3]
	}
	for _, name := range names {
		if err := admin.RegisterAdminRoute("POST", base+"/"+name, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.RawQuery != "" {
				writeError(w, ErrInvalidRequest)
				return
			}
			if name == "preview" {
				var body struct {
					Definition json.RawMessage `json:"definition"`
					Scenario   string          `json:"scenario"`
				}
				if !readJSON(w, r, &body) {
					return
				}
				value, err := s.PreviewAI(r.Context(), body.Definition, body.Scenario)
				writeAdminValue(w, value, err)
				return
			}
			key, ok := requestKey(w, r)
			if !ok {
				return
			}
			var result MutationResult
			var err error
			switch name {
			case "settings":
				var body AISettings
				if !readJSON(w, r, &body) {
					return
				}
				result, err = s.SetAIEnabled(r.Context(), key, body)
			case "policies":
				var body SaveAIPolicy
				if !readJSON(w, r, &body) {
					return
				}
				result, err = s.SaveAIPolicy(r.Context(), key, body)
			case "bots":
				var body SaveAIBot
				if !readJSON(w, r, &body) {
					return
				}
				result, err = s.SaveAIBot(r.Context(), key, body)
			}
			writeMutation(w, result, err)
		})); err != nil {
			return err
		}
	}
	return nil
}
