package imageactivity

import (
	"bytes"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// pointerParts accepts only bounded JSON Pointers. The empty pointer denotes
// the current value and is allowed for response extraction only.
func pointerParts(raw string, root bool) ([]string, error) {
	if len(raw) > 512 || !utf8.ValidString(raw) {
		return nil, ErrInvalid
	}
	if raw == "" {
		if root {
			return nil, nil
		}
		return nil, ErrInvalid
	}
	if raw[0] != '/' {
		return nil, ErrInvalid
	}
	parts := strings.Split(raw[1:], "/")
	if len(parts) > 32 {
		return nil, ErrInvalid
	}
	for i, p := range parts {
		if len(p) == 0 || len(p) > 256 {
			return nil, ErrInvalid
		}
		for j := 0; j < len(p); j++ {
			if p[j] == '~' {
				j++
				if j == len(p) || (p[j] != '0' && p[j] != '1') {
					return nil, ErrInvalid
				}
			}
		}
		p = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
		if !safeText(p, 256) {
			return nil, ErrInvalid
		}
		parts[i] = p
	}
	return parts, nil
}
func safeText(v string, max int) bool {
	if !utf8.ValidString(v) || len(v) > max {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
func keyValid(k ParameterKey) bool {
	switch k {
	case Prompt, NegativePrompt, N, Size, AspectRatio, Resolution, Seed, Steps, Guidance, Quality:
		return true
	}
	return false
}
func validRelativePath(path string, poll bool) bool {
	if !safeText(path, 512) || strings.Contains(path, "\\") {
		return false
	}
	count := strings.Count(path, "{task_id}")
	if poll && count != 1 || !poll && count != 0 {
		return false
	}
	test := strings.ReplaceAll(path, "{task_id}", "opaque")
	if strings.ContainsAny(test, "{}") || strings.HasPrefix(test, "//") {
		return false
	}
	u, err := url.Parse(test)
	if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" || u.Fragment != "" || u.Path == "" {
		return false
	}
	if queryAt := strings.IndexByte(path, '?'); poll && queryAt >= 0 && strings.Contains(path[queryAt+1:], "{task_id}") {
		return false
	}
	return true
}
func validateMapping(m Mapping, optional bool) error {
	if optional && m.ModelPointer == "" && len(m.Parameters) == 0 && len(m.Constants) == 0 {
		return nil
	}
	if len(m.Parameters) > 10 || len(m.Constants) > 64 {
		return ErrInvalid
	}
	all := make([][]string, 0, len(m.Parameters)+len(m.Constants)+1)
	add := func(p string) error {
		parts, err := pointerParts(p, false)
		if err != nil {
			return err
		}
		for _, prev := range all {
			n := len(parts)
			if len(prev) < n {
				n = len(prev)
			}
			same := true
			for i := 0; i < n; i++ {
				if prev[i] != parts[i] {
					same = false
					break
				}
			}
			if same {
				return ErrInvalid
			}
		}
		all = append(all, parts)
		return nil
	}
	if err := add(m.ModelPointer); err != nil {
		return err
	}
	for key, p := range m.Parameters {
		if !keyValid(key) {
			return ErrInvalid
		}
		if err := add(p); err != nil {
			return err
		}
	}
	if _, ok := m.Parameters[Prompt]; !ok {
		return ErrInvalid
	}
	for _, c := range m.Constants {
		if err := add(c.Pointer); err != nil {
			return err
		}
		if len(c.Value) > 4096 {
			return ErrInvalid
		}
		var v any
		d := json.NewDecoder(bytes.NewReader(c.Value))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			return ErrInvalid
		}
		switch v.(type) {
		case nil, string, bool, json.Number:
		default:
			return ErrInvalid
		}
		if !json.Valid(c.Value) {
			return ErrInvalid
		}
	}
	encoded, _ := json.Marshal(m)
	if len(encoded) > 65536 {
		return ErrInvalid
	}
	return nil
}
func ValidateAdapter(a Adapter) error {
	raw, err := json.Marshal(a)
	if err != nil || len(raw) > 262144 {
		return ErrInvalid
	}
	if a.Discovery.Method != "GET" || a.Submit.Method != "POST" || !validRelativePath(a.Discovery.Path, false) || !validRelativePath(a.Submit.Path, false) {
		return ErrInvalid
	}
	for _, p := range []string{a.Discovery.ItemsPointer, a.Discovery.IDPointer, a.Response.ImagesPointer} {
		if _, err = pointerParts(p, true); err != nil {
			return err
		}
	}
	if a.Discovery.MetadataPointer != nil {
		if _, err = pointerParts(*a.Discovery.MetadataPointer, true); err != nil {
			return err
		}
	}
	if err = validateMapping(a.Submit.Mapping, false); err != nil {
		return err
	}
	if a.Response.Base64Pointer == nil && a.Response.URLPointer == nil {
		return ErrInvalid
	}
	for _, p := range []*string{a.Response.Base64Pointer, a.Response.URLPointer, a.Response.TaskIDPointer, a.Response.StatePointer} {
		if p != nil {
			if _, err = pointerParts(*p, true); err != nil {
				return err
			}
		}
	}
	seen := map[string]bool{}
	for _, list := range [][]string{a.Response.WorkingStates, a.Response.SuccessStates, a.Response.FailureStates} {
		if len(list) > 32 {
			return ErrInvalid
		}
		for _, v := range list {
			if len(v) == 0 || !safeText(v, 128) || seen[v] {
				return ErrInvalid
			}
			seen[v] = true
		}
	}
	if a.Poll != nil {
		if a.Poll.Method != "GET" || !validRelativePath(a.Poll.Path, true) || a.Response.TaskIDPointer == nil || a.Response.StatePointer == nil || len(a.Response.WorkingStates) == 0 || len(a.Response.SuccessStates) == 0 || len(a.Response.FailureStates) == 0 {
			return ErrInvalid
		}
	} else if a.Response.TaskIDPointer != nil {
		return ErrInvalid
	}
	return nil
}
func requestURL(base, path, id string) (string, error) {
	if !validRelativePath(path, id != "") {
		return "", ErrInvalid
	}
	if id != "" {
		if len(id) > 2048 || !safeText(id, 2048) {
			return "", ErrInvalid
		}
		// Encode every byte, including dots, so a task identifier cannot become a
		// dot segment or change the path, query, or authority during resolution.
		const hex = "0123456789ABCDEF"
		var escaped strings.Builder
		escaped.Grow(len(id) * 3)
		for i := 0; i < len(id); i++ {
			escaped.WriteByte('%')
			escaped.WriteByte(hex[id[i]>>4])
			escaped.WriteByte(hex[id[i]&15])
		}
		path = strings.Replace(path, "{task_id}", escaped.String(), 1)
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", ErrInvalid
	}
	if !strings.HasSuffix(b.Path, "/") {
		b.Path += "/"
	}
	p, err := url.Parse(path)
	if err != nil {
		return "", ErrInvalid
	}
	resolved := b.ResolveReference(p)
	if resolved.Scheme != b.Scheme || resolved.Host != b.Host || resolved.User != nil || resolved.Fragment != "" {
		return "", ErrInvalid
	}
	return resolved.String(), nil
}
func setPointer(root map[string]any, pointer string, value any) error {
	parts, err := pointerParts(pointer, false)
	if err != nil {
		return err
	}
	current := root
	for _, key := range parts[:len(parts)-1] {
		v, ok := current[key]
		if !ok {
			next := map[string]any{}
			current[key] = next
			current = next
			continue
		}
		next, ok := v.(map[string]any)
		if !ok {
			return ErrInvalid
		}
		current = next
	}
	key := parts[len(parts)-1]
	if _, ok := current[key]; ok {
		return ErrInvalid
	}
	current[key] = value
	return nil
}
func buildRequest(model string, params map[ParameterKey]any, mapping Mapping) ([]byte, error) {
	if err := validateMapping(mapping, false); err != nil {
		return nil, err
	}
	root := map[string]any{}
	if err := setPointer(root, mapping.ModelPointer, model); err != nil {
		return nil, err
	}
	for key, value := range params {
		pointer, ok := mapping.Parameters[key]
		if !ok {
			return nil, ErrInvalid
		}
		if err := setPointer(root, pointer, value); err != nil {
			return nil, err
		}
	}
	for _, c := range mapping.Constants {
		var value any
		d := json.NewDecoder(bytes.NewReader(c.Value))
		d.UseNumber()
		if d.Decode(&value) != nil {
			return nil, ErrInvalid
		}
		if err := setPointer(root, c.Pointer, value); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(root)
	if err != nil || len(encoded) > maxJSON {
		return nil, ErrInvalid
	}
	return encoded, nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func safeInteger(v float64) bool {
	return finite(v) && math.Trunc(v) == v && math.Abs(v) <= 9007199254740991
}
func decimalRevision(v string, allowZero bool) (int64, error) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 || (!allowZero && n == 0) || strconv.FormatInt(n, 10) != v {
		return 0, ErrInvalid
	}
	return n, nil
}
