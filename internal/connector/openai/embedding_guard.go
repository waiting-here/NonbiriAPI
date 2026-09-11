package openai

import "encoding/json"

// containsEmbeddingProjection uses the same literal and semantic detectors as
// chat, with only the string channels in the validated public projection.
// A general JSON token decoder would retain a second large token buffer for
// a base64 vector even though its numeric payload has already been checked.
func (g *responseGuard) containsEmbeddingProjection(wire []byte) bool {
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
	if g.containsEmbeddingString("/object", root["object"]) || g.containsEmbeddingString("/model", root["model"]) {
		return true
	}
	return !visitJSONArray(root["data"], func(raw []byte) bool {
		fields, ok := borrowedObjectFields(raw)
		if !ok {
			return false
		}
		item := fieldsByName(fields)
		if g.containsEmbeddingString("/data/[]/object", item["object"]) {
			return false
		}
		vector := item["embedding"]
		return len(vector) > 0 && (vector[0] != '"' || !g.containsEmbeddingString("/data/[]/embedding", vector))
	})
}

func (g *responseGuard) containsEmbeddingString(path string, raw []byte) bool {
	var value string
	if len(raw) < 2 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return true
	}
	guard, err := g.guardForPath(path)
	if err != nil {
		return true
	}
	// Scan the decoded string directly. A []byte conversion would double a
	// near-limit vector's transient memory merely to feed the same detector.
	for i := 0; i < len(value); i++ {
		for _, detector := range guard.detectors {
			if detector.push(value[i]) {
				guard.matched = true
				g.matched = true
				return true
			}
		}
	}
	return false
}
