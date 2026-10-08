package openai

// Scan only the string channels in an already validated projection. This
// avoids a JSON token decoder retaining another buffer for a large image.
func (g *responseGuard) containsImageProjection(wire []byte, stream bool) bool {
	if g == nil || g.matched {
		return g != nil && g.matched
	}
	if g.ContainsBytes(wire) {
		return true
	}
	fields, ok := borrowedObjectFields(wire)
	if !ok {
		return true
	}
	root := fieldsByName(fields)
	for _, name := range []string{"type", "background", "output_format", "quality", "size"} {
		if raw := root[name]; len(raw) != 0 && g.containsEmbeddingString("/"+name, raw) {
			return true
		}
	}
	if stream {
		return g.containsEmbeddingString("/b64_json", root["b64_json"])
	}
	return !visitJSONArray(root["data"], func(raw []byte) bool {
		fields, ok := borrowedObjectFields(raw)
		if !ok {
			return false
		}
		for _, field := range fields {
			if g.containsEmbeddingString("/data/[]/"+field.name, field.value) {
				return false
			}
		}
		return true
	})
}
