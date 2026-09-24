package imageactivity

import (
	"bytes"
	"encoding/json"
	"strconv"
)

func whitespace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\n' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	return i
}
func stringEnd(b []byte, i int) (int, error) {
	if i >= len(b) || b[i] != '"' {
		return 0, ErrInvalid
	}
	i++
	for i < len(b) {
		switch b[i] {
		case '"':
			return i + 1, nil
		case '\\':
			i++
			if i >= len(b) {
				return 0, ErrInvalid
			}
			if b[i] == 'u' {
				i += 4
			}
		default:
			if b[i] < 32 {
				return 0, ErrInvalid
			}
		}
		i++
	}
	return 0, ErrInvalid
}
func valueEnd(b []byte, i, depth int) (int, error) {
	i = whitespace(b, i)
	if i >= len(b) || depth > 32 {
		return 0, ErrInvalid
	}
	switch b[i] {
	case '"':
		return stringEnd(b, i)
	case '{':
		i = whitespace(b, i+1)
		if i < len(b) && b[i] == '}' {
			return i + 1, nil
		}
		keys := map[string]bool{}
		for {
			end, err := stringEnd(b, i)
			if err != nil || end-i > 1026 || len(keys) >= 1024 {
				return 0, ErrInvalid
			}
			var name string
			if json.Unmarshal(b[i:end], &name) != nil || keys[name] {
				return 0, ErrInvalid
			}
			keys[name] = true
			i = whitespace(b, end)
			if i >= len(b) || b[i] != ':' {
				return 0, ErrInvalid
			}
			i, err = valueEnd(b, i+1, depth+1)
			if err != nil {
				return 0, err
			}
			i = whitespace(b, i)
			if i >= len(b) {
				return 0, ErrInvalid
			}
			if b[i] == '}' {
				return i + 1, nil
			}
			if b[i] != ',' {
				return 0, ErrInvalid
			}
			i = whitespace(b, i+1)
		}
	case '[':
		i = whitespace(b, i+1)
		if i < len(b) && b[i] == ']' {
			return i + 1, nil
		}
		for {
			end, err := valueEnd(b, i, depth+1)
			if err != nil {
				return 0, err
			}
			i = whitespace(b, end)
			if i >= len(b) {
				return 0, ErrInvalid
			}
			if b[i] == ']' {
				return i + 1, nil
			}
			if b[i] != ',' {
				return 0, ErrInvalid
			}
			i = whitespace(b, i+1)
		}
	default:
		end := i
		for end < len(b) && b[end] != ',' && b[end] != ']' && b[end] != '}' && b[end] != ' ' && b[end] != '\n' && b[end] != '\r' && b[end] != '\t' {
			end++
		}
		if end == i || !json.Valid(b[i:end]) {
			return 0, ErrInvalid
		}
		return end, nil
	}
}
func rawPointer(data []byte, pointer string, partial bool) ([]byte, error) {
	parts, err := pointerParts(pointer, true)
	if err != nil {
		return nil, err
	}
	return rawPath(data, parts, partial, 0)
}
func rawPath(data []byte, parts []string, partial bool, depth int) ([]byte, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || depth > 32 {
		return nil, ErrInvalid
	}
	if len(parts) == 0 {
		end, err := valueEnd(data, 0, depth)
		if err != nil {
			if partial && data[0] == '[' {
				return data, nil
			}
			return nil, err
		}
		return data[:end], nil
	}
	switch data[0] {
	case '{':
		i := whitespace(data, 1)
		for i < len(data) && data[i] != '}' {
			end, err := stringEnd(data, i)
			if err != nil || end-i > 1026 {
				return nil, ErrInvalid
			}
			var key string
			if json.Unmarshal(data[i:end], &key) != nil {
				return nil, ErrInvalid
			}
			i = whitespace(data, end)
			if i >= len(data) || data[i] != ':' {
				return nil, ErrInvalid
			}
			i = whitespace(data, i+1)
			if key == parts[0] {
				return rawPath(data[i:], parts[1:], partial, depth+1)
			}
			i, err = valueEnd(data, i, depth+1)
			if err != nil {
				return nil, err
			}
			i = whitespace(data, i)
			if i < len(data) && data[i] == ',' {
				i = whitespace(data, i+1)
			} else {
				break
			}
		}
	case '[':
		target, err := strconv.Atoi(parts[0])
		if err != nil || target < 0 || strconv.Itoa(target) != parts[0] || target > 1000 {
			return nil, ErrInvalid
		}
		i := whitespace(data, 1)
		for index := 0; i < len(data) && data[i] != ']'; index++ {
			if index == target {
				return rawPath(data[i:], parts[1:], partial, depth+1)
			}
			i, err = valueEnd(data, i, depth+1)
			if err != nil {
				return nil, err
			}
			i = whitespace(data, i)
			if i < len(data) && data[i] == ',' {
				i = whitespace(data, i+1)
			} else {
				break
			}
		}
	}
	return nil, ErrNotFound
}
func rawArray(data []byte, limit int) ([][]byte, error) {
	out := [][]byte{}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '[' {
		return out, ErrInvalid
	}
	i := whitespace(data, 1)
	for i < len(data) && data[i] != ']' {
		if len(out) >= limit {
			return out, ErrCapacity
		}
		end, err := valueEnd(data, i, 1)
		if err != nil {
			return out, err
		}
		if !json.Valid(data[i:end]) {
			return out, ErrInvalid
		}
		out = append(out, data[i:end])
		i = whitespace(data, end)
		if i < len(data) && data[i] == ',' {
			i = whitespace(data, i+1)
		} else {
			break
		}
	}
	if i >= len(data) || data[i] != ']' {
		return out, ErrInvalid
	}
	return out, nil
}
func rawString(data []byte, pointer string, max int) (string, error) {
	raw, err := rawPointer(data, pointer, false)
	if err != nil {
		return "", err
	}
	if len(raw) > max*6+2 {
		return "", ErrInvalid
	}
	var out string
	if json.Unmarshal(raw, &out) != nil || len(out) == 0 || !safeText(out, max) {
		return "", ErrInvalid
	}
	return out, nil
}
