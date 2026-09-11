package openai

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

const maxEmbeddingJSONDepth = 64

// boundedJSON checks decoded duplicate keys without building an object tree
// or retaining scalar array elements. Key accounting bounds even hostile
// unknown objects while leaving the vector's element count byte-bounded.
type boundedJSON struct {
	data     []byte
	pos      int
	keyBytes int
}

func validateEmbeddingJSON(data []byte) bool {
	if !utf8.Valid(data) || !json.Valid(data) {
		return false
	}
	p := boundedJSON{data: data}
	return p.value(1)
}

func (p *boundedJSON) space() {
	for p.pos < len(p.data) && (p.data[p.pos] == ' ' || p.data[p.pos] == '\n' || p.data[p.pos] == '\r' || p.data[p.pos] == '\t') {
		p.pos++
	}
}

// stringEnd is used only after json.Valid has established lexical validity.
func jsonStringEnd(data []byte, start int) int {
	for i := start + 1; i < len(data); i++ {
		if data[i] == '\\' {
			i++
		} else if data[i] == '"' {
			return i + 1
		}
	}
	return len(data)
}

func (p *boundedJSON) value(depth int) bool {
	p.space()
	switch p.data[p.pos] {
	case '{':
		if depth > maxEmbeddingJSONDepth {
			return false
		}
		p.pos++
		seen := make(map[string]struct{})
		owned := 0
		defer func() { p.keyBytes -= owned }()
		p.space()
		for p.data[p.pos] != '}' {
			end := jsonStringEnd(p.data, p.pos)
			var key string
			if json.Unmarshal(p.data[p.pos:end], &key) != nil || !validFieldName(key) {
				return false
			}
			if _, found := seen[key]; found || depth == 1 && len(seen) >= maxTopLevelFields {
				return false
			}
			cost := len(key) + 128
			if p.keyBytes+cost > 16<<20 {
				return false
			}
			p.keyBytes += cost
			owned += cost
			seen[key] = struct{}{}
			p.pos = end
			p.space()
			p.pos++ // colon
			if !p.value(depth + 1) {
				return false
			}
			p.space()
			if p.data[p.pos] == ',' {
				p.pos++
				p.space()
			}
		}
		p.pos++
	case '[':
		if depth > maxEmbeddingJSONDepth {
			return false
		}
		p.pos++
		p.space()
		for p.data[p.pos] != ']' {
			if !p.value(depth + 1) {
				return false
			}
			p.space()
			if p.data[p.pos] == ',' {
				p.pos++
			}
		}
		p.pos++
	case '"':
		p.pos = jsonStringEnd(p.data, p.pos)
	default:
		for p.pos < len(p.data) && !bytes.ContainsRune([]byte(",]} \n\r\t"), rune(p.data[p.pos])) {
			p.pos++
		}
	}
	return true
}

// jsonValueEnd returns a borrowed span boundary in already validated JSON.
// Delimiters inside escaped strings cannot affect container nesting.
func jsonValueEnd(data []byte, start int) int {
	if data[start] == '"' {
		return jsonStringEnd(data, start)
	}
	if data[start] != '{' && data[start] != '[' {
		end := start
		for end < len(data) && !bytes.ContainsRune([]byte(",]} \n\r\t"), rune(data[end])) {
			end++
		}
		return end
	}
	depth := 0
	for i := start; i < len(data); i++ {
		switch data[i] {
		case '"':
			i = jsonStringEnd(data, i) - 1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(data)
}

// borrowedObjectFields never copies values. Its caller keeps the validated
// input alive and must not call clearFields on these aliases.
func borrowedObjectFields(data []byte) ([]jsonField, bool) {
	data = bytes.TrimSpace(data)
	if len(data) < 2 || data[0] != '{' {
		return nil, false
	}
	p := boundedJSON{data: data, pos: 1}
	var fields []jsonField
	p.space()
	for p.data[p.pos] != '}' {
		if len(fields) >= maxTopLevelFields {
			return nil, false
		}
		end := jsonStringEnd(data, p.pos)
		var name string
		if json.Unmarshal(data[p.pos:end], &name) != nil {
			return nil, false
		}
		p.pos = end
		p.space()
		p.pos++
		p.space()
		end = jsonValueEnd(data, p.pos)
		fields = append(fields, jsonField{name: name, value: data[p.pos:end]})
		p.pos = end
		p.space()
		if data[p.pos] == ',' {
			p.pos++
			p.space()
		}
	}
	return fields, true
}

func visitJSONArray(data []byte, visit func([]byte) bool) bool {
	data = bytes.TrimSpace(data)
	if len(data) < 2 || data[0] != '[' {
		return false
	}
	p := boundedJSON{data: data, pos: 1}
	p.space()
	for data[p.pos] != ']' {
		end := jsonValueEnd(data, p.pos)
		if !visit(data[p.pos:end]) {
			return false
		}
		p.pos = end
		p.space()
		if data[p.pos] == ',' {
			p.pos++
			p.space()
		}
	}
	return true
}
