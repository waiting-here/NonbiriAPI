// Package transportpolicy defines a model's upstream chat transport choice.
package transportpolicy

type Rule string

const (
	Passthrough    Rule = "passthrough"
	ForceNonStream Rule = "force_non_stream"
	ForceStream    Rule = "force_stream"
)

func (r Rule) Valid() bool {
	return r == Passthrough || r == ForceNonStream || r == ForceStream
}

func (r Rule) UpstreamStream(callerStream bool) bool {
	switch r {
	case ForceNonStream:
		return false
	case ForceStream:
		return true
	default:
		return callerStream
	}
}
