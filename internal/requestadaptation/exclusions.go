package requestadaptation

// NormalizeExcludedRequestFields validates a finite, case-sensitive set of
// top-level names. Core routing, input and response protocol fields cannot be
// excluded. A returned slice is owned by the caller.
func NormalizeExcludedRequestFields(fields []string) ([]string, error) {
	if len(fields) > 32 {
		return nil, ErrInvalid
	}
	result := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, name := range fields {
		if len(name) < 1 || len(name) > 64 {
			return nil, ErrInvalid
		}
		for index := range len(name) {
			ch := name[index]
			if ch != '_' && !(ch >= 'A' && ch <= 'Z') && !(ch >= 'a' && ch <= 'z') && !(index > 0 && ch >= '0' && ch <= '9') {
				return nil, ErrInvalid
			}
		}
		switch name {
		case "model", "messages", "input", "prompt", "stream", "stream_options", "tools", "tool_choice", "functions", "function_call", "response_format", "encoding_format":
			return nil, ErrInvalid
		}
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result, nil
}
