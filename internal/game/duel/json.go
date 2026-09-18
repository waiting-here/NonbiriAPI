package duel

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// Decode rejects duplicate keys, unknown/case-mismatched fields, excessive
// nesting, invalid UTF-8 and trailing values before decoding typed data.
func Decode(body []byte, out any) error {
	if err := validateJSON(body); err != nil {
		return err
	}
	typ := reflect.TypeOf(out)
	if typ == nil || typ.Kind() != reflect.Pointer || typ.Elem().Kind() != reflect.Struct {
		return ErrInvalidRequest
	}
	if err := exactJSONFields(body, typ.Elem()); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return ErrInvalidRequest
	}
	return nil
}

func validateJSON(body []byte) error {
	if len(body) == 0 || len(body) > 1<<20 || !utf8.Valid(body) || bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return ErrInvalidRequest
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return ErrInvalidRequest
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return ErrInvalidRequest
					}
					seen[name] = true
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
			default:
				return ErrInvalidRequest
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if walk(0) != nil {
		return ErrInvalidRequest
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalidRequest
	}
	return nil
}

func exactJSONFields(body []byte, typ reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return nil
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(body, &object) != nil || object == nil {
			return ErrInvalidRequest
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" || !field.IsExported() {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		for key, value := range object {
			child, ok := fields[key]
			if !ok {
				return ErrInvalidRequest
			}
			if err := exactJSONFields(value, child); err != nil {
				return err
			}
		}
	case reflect.Array, reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var items []json.RawMessage
		if json.Unmarshal(body, &items) != nil {
			return ErrInvalidRequest
		}
		if typ.Kind() == reflect.Array && len(items) != typ.Len() {
			return ErrInvalidRequest
		}
		for _, value := range items {
			if err := exactJSONFields(value, typ.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		var object map[string]json.RawMessage
		if json.Unmarshal(body, &object) != nil {
			return ErrInvalidRequest
		}
		for _, value := range object {
			if err := exactJSONFields(value, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func HasFields(body []byte, names ...string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return false
	}
	for _, name := range names {
		if _, ok := object[name]; !ok {
			return false
		}
	}
	return true
}
