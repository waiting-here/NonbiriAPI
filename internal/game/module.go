package game

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// ConfigValue exposes fresh projections of a module-owned compiled snapshot.
// Callers cannot mutate the configuration retained by the codec.
type ConfigValue interface {
	Enabled() bool
	NeedsReady() bool
	Raw() map[string]string
	Wire() json.RawMessage
	UserWire(available func(mode, spec string) bool) json.RawMessage
}

type ConfigCodec interface {
	Keys() []string
	ValidatePatch(json.RawMessage) error
	Compile(map[string]string) (ConfigValue, error)
	CompileWire(json.RawMessage, bool) (ConfigValue, error)
	Merge(ConfigValue, json.RawMessage, bool) (ConfigValue, error)
}

type RouteDeclaration struct {
	Continuation bool
	Station      string
	Method       string
	Pattern      string
}

// ModuleDescriptor contains only identity and declared capabilities. Concrete
// rules, persistence and worker construction belong to the registering module.
type ModuleDescriptor struct {
	ID               string
	Version          int
	StableOrder      int
	ResourcePrefixes []string
	Modes            []string
	Specs            []string
	BoardIDs         []string
	Routes           []RouteDeclaration
	ContinuationIDs  []string
	HomeRouteID      string
	SnapshotFields   []string
	Codec            ConfigCodec
}

func (module ModuleDescriptor) ResolveMode(mode string) error {
	if !slices.Contains(module.Modes, mode) {
		return ErrUnknownMode
	}
	return nil
}

func (module ModuleDescriptor) ResolveSpec(spec string) error {
	if !slices.Contains(module.Specs, spec) {
		return ErrUnknownSpec
	}
	return nil
}

func (module ModuleDescriptor) ResolveBoard(board string) error {
	if !slices.Contains(module.BoardIDs, board) {
		return ErrUnknownBoard
	}
	return nil
}

func (module ModuleDescriptor) ValidResource(value string) bool {
	for _, prefix := range module.ResourcePrefixes {
		if validOpaqueID(value, prefix) {
			return true
		}
	}
	return false
}

func cloneDescriptor(module ModuleDescriptor) ModuleDescriptor {
	module.ResourcePrefixes = slices.Clone(module.ResourcePrefixes)
	module.Modes = slices.Clone(module.Modes)
	module.Specs = slices.Clone(module.Specs)
	module.BoardIDs = slices.Clone(module.BoardIDs)
	module.Routes = slices.Clone(module.Routes)
	module.ContinuationIDs = slices.Clone(module.ContinuationIDs)
	module.SnapshotFields = slices.Clone(module.SnapshotFields)
	return module
}

type Registry struct {
	modules []ModuleDescriptor
	sealed  bool
}

type declaredCodec struct {
	ConfigCodec
	keys []string
}

func (codec declaredCodec) Keys() []string { return slices.Clone(codec.keys) }

func NewRegistry() *Registry { return &Registry{} }

func (registry *Registry) Register(module ModuleDescriptor) error {
	if registry == nil || registry.sealed || module.ID == "" || module.Version < 1 || module.StableOrder < 0 || module.Codec == nil || module.HomeRouteID == "" || len(module.ResourcePrefixes) == 0 {
		return ErrInvalidContract
	}
	for _, c := range module.ID {
		if c < 'a' || c > 'z' {
			return ErrInvalidContract
		}
	}
	for _, current := range registry.modules {
		if current.ID == module.ID || current.StableOrder == module.StableOrder || current.HomeRouteID == module.HomeRouteID {
			return ErrInvalidContract
		}
	}
	module.Codec = declaredCodec{ConfigCodec: module.Codec, keys: slices.Clone(module.Codec.Keys())}
	registry.modules = append(registry.modules, cloneDescriptor(module))
	return nil
}

// Seal checks all cross-module names before any route or worker is installed.
func (registry *Registry) Seal() error {
	if registry == nil || registry.sealed || len(registry.modules) == 0 {
		return ErrInvalidContract
	}
	seen := make(map[string]bool)
	muxes := map[string]*http.ServeMux{"user": http.NewServeMux(), "admin": http.NewServeMux()}
	claim := func(namespace, value string) error {
		key := namespace + ":" + value
		if value == "" || seen[key] {
			return fmt.Errorf("%w: duplicate or empty %s", ErrInvalidContract, namespace)
		}
		seen[key] = true
		return nil
	}
	for _, module := range registry.modules {
		for _, field := range module.SnapshotFields {
			if slices.Contains([]string{"server_now", "balance", "games_enabled", "revision", "master_enabled"}, field) {
				return ErrInvalidContract
			}
			for _, other := range registry.modules {
				if field == other.ID {
					return ErrInvalidContract
				}
			}
			if err := claim("snapshot", field); err != nil {
				return err
			}
		}
		for _, prefix := range module.ResourcePrefixes {
			if !strings.HasSuffix(prefix, "_") || strings.ContainsAny(prefix, " /\\{}") {
				return ErrInvalidContract
			}
			if err := claim("prefix", prefix); err != nil {
				return err
			}
		}
		for _, key := range module.Codec.Keys() {
			if key == GamesEnabledKey || !strings.HasPrefix(key, "game_"+module.ID+"_") {
				return ErrInvalidContract
			}
			if err := claim("config", key); err != nil {
				return err
			}
		}
		for _, value := range module.BoardIDs {
			if err := claim("board", value); err != nil {
				return err
			}
		}
		for _, value := range module.ContinuationIDs {
			if err := claim("continuation", value); err != nil {
				return err
			}
		}
		for _, value := range module.Modes {
			if err := claim(module.ID+"/mode", value); err != nil {
				return err
			}
		}
		for _, value := range module.Specs {
			if err := claim(module.ID+"/spec", value); err != nil {
				return err
			}
		}
		for _, route := range module.Routes {
			if route.Station != "user" && route.Station != "admin" || !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, route.Method) || !strings.HasPrefix(route.Pattern, "/") || strings.ContainsAny(route.Pattern, " ?#") {
				return ErrInvalidContract
			}
			if err := validateRoutePattern(muxes[route.Station], route); err != nil {
				return err
			}
			parts := strings.Split(route.Pattern, "/")
			for i, part := range parts {
				if strings.HasPrefix(part, "{") {
					parts[i] = "{}"
				}
			}
			if err := claim("route", route.Station+" "+route.Method+" "+strings.Join(parts, "/")); err != nil {
				return err
			}
		}
	}
	slices.SortFunc(registry.modules, func(a, b ModuleDescriptor) int { return a.StableOrder - b.StableOrder })
	registry.sealed = true
	return nil
}

func validateRoutePattern(mux *http.ServeMux, route RouteDeclaration) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrInvalidContract
		}
	}()
	mux.HandleFunc(route.Method+" "+route.Pattern, func(http.ResponseWriter, *http.Request) {})
	return nil
}

func (registry *Registry) Sealed() bool { return registry != nil && registry.sealed }

func (registry *Registry) Descriptors() []ModuleDescriptor {
	if !registry.Sealed() {
		return nil
	}
	result := make([]ModuleDescriptor, len(registry.modules))
	for i, module := range registry.modules {
		result[i] = cloneDescriptor(module)
	}
	return result
}

func (registry *Registry) Resolve(id string, version int) (ModuleDescriptor, error) {
	for _, module := range registry.Descriptors() {
		if module.ID == id && module.Version == version {
			return module, nil
		}
	}
	return ModuleDescriptor{}, ErrUnknownGame
}
