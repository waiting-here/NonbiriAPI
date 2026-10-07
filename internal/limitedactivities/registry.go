package limitedactivities

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"sort"
)

type definition struct {
	key, name, coverKey string
	runtime             Runtime
	configuration       moduleConfiguration
}

// Module configuration is compiled code. Public projection cannot accidentally
// return an engine's upstream, secret or execution configuration.
type moduleConfiguration interface {
	Normalize(json.RawMessage) (json.RawMessage, error)
	PublicTx(context.Context, *sql.Tx, string, json.RawMessage) (json.RawMessage, error)
	ApplyTx(context.Context, *sql.Tx, string, json.RawMessage) error
}

// Registry is fixed during application composition. Database configuration
// cannot install executable modules, scripts or arbitrary business routes.
type Registry struct {
	modules map[string]definition
	keys    []string
}

func NewRegistry(pictureBook Runtime, fatFish ...Runtime) *Registry {
	if len(fatFish) > 1 {
		return nil
	}
	r := &Registry{modules: map[string]definition{}}
	r.modules[PictureBook] = definition{PictureBook, "喵帕斯的绘本", "picture-book", pictureBook, pictureBookConfiguration{}}
	var fishRuntime Runtime
	if len(fatFish) == 1 {
		fishRuntime = fatFish[0]
	}
	r.modules[FatFish] = definition{FatFish, "饲养大肥鱼", "fat-fish", fishRuntime, emptyConfiguration{}}
	for key := range r.modules {
		r.keys = append(r.keys, key)
	}
	sort.Strings(r.keys)
	return r
}

func (r *Registry) lookup(key string) (definition, error) {
	if r == nil {
		return definition{}, ErrNotFound
	}
	d, ok := r.modules[key]
	if !ok {
		return definition{}, ErrNotFound
	}
	return d, nil
}

func (d definition) ready(ctx context.Context, tx *sql.Tx) (bool, error) {
	if nilInterface(d.runtime) {
		return false, nil
	}
	return d.runtime.ReadyTx(ctx, tx)
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
