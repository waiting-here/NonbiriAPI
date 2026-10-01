// Package rolepolicy defines bounded model-owned handling of extra message roles.
package rolepolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxBytes = 8192

var ErrInvalid = errors.New("role policy: invalid configuration")

type Policy struct {
	DefaultAction string            `json:"default_action"`
	Rules         map[string]string `json:"rules"`
}

func Default() Policy { return Policy{DefaultAction: "native", Rules: map[string]string{}} }
func (p Policy) Clone() Policy {
	out := Policy{DefaultAction: p.DefaultAction, Rules: make(map[string]string, len(p.Rules))}
	for k, v := range p.Rules {
		out.Rules[k] = v
	}
	return out
}
func ValidAction(s string) bool {
	switch s {
	case "native", "passthrough", "system", "user", "assistant", "reject":
		return true
	}
	return false
}
func CoreRole(s string) bool {
	switch s {
	case "system", "user", "assistant", "tool", "function":
		return true
	}
	return false
}
func validName(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) < 1 || utf8.RuneCountInString(s) > 64 || len(s) > 256 || strings.TrimSpace(s) != s || CoreRole(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (p Policy) Canonical() (string, error) {
	if !ValidAction(p.DefaultAction) || p.Rules == nil || len(p.Rules) > 32 {
		return "", ErrInvalid
	}
	for role, action := range p.Rules {
		if !validName(role) || !ValidAction(action) {
			return "", ErrInvalid
		}
	}
	encoded, err := json.Marshal(p)
	if err != nil || len(encoded) > MaxBytes {
		return "", ErrInvalid
	}
	return string(encoded), nil
}
func (p Policy) IsNative() bool {
	if p.DefaultAction != "native" {
		return false
	}
	for _, action := range p.Rules {
		if action != "native" {
			return false
		}
	}
	return true
}
func (p Policy) Action(role string) string {
	if CoreRole(role) {
		return "native"
	}
	if action, ok := p.Rules[role]; ok {
		return action
	}
	return p.DefaultAction
}
func Decode(encoded string) (Policy, error) {
	var p Policy
	err := p.UnmarshalJSON([]byte(encoded))
	return p, err
}
func (p *Policy) UnmarshalJSON(encoded []byte) error {
	if len(encoded) > MaxBytes || !utf8.Valid(encoded) {
		return ErrInvalid
	}
	fields, err := object(encoded, 2)
	if err != nil || len(fields) != 2 {
		return ErrInvalid
	}
	var out Policy
	if json.Unmarshal(fields["default_action"], &out.DefaultAction) != nil {
		return ErrInvalid
	}
	rules, err := object(fields["rules"], 32)
	if err != nil {
		return ErrInvalid
	}
	out.Rules = make(map[string]string, len(rules))
	for k, raw := range rules {
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return ErrInvalid
		}
		out.Rules[k] = v
	}
	if _, err = out.Canonical(); err != nil {
		return err
	}
	*p = out
	return nil
}
func object(raw []byte, limit int) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, ErrInvalid
	}
	out := make(map[string]json.RawMessage)
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || len(out) >= limit {
			return nil, ErrInvalid
		}
		if _, exists := out[key]; exists {
			return nil, ErrInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, ErrInvalid
		}
		out[key] = value
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') {
		return nil, ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrInvalid
	}
	return out, nil
}
