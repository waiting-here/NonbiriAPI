package openai

// cumulativeUsage tracks snapshots for one response. Prompt accounting is
// fixed for that response; completion tokens may grow as output is generated.
// Repeated snapshots replace the previous value and are never summed.
type cumulativeUsage struct {
	value    Usage
	poisoned bool
}

func (u *cumulativeUsage) observe(next Usage, malformed bool) Usage {
	if malformed || (next.Present && u.value.Present && (next.UncachedInputTokens != u.value.UncachedInputTokens ||
		next.CacheWriteInputTokens != u.value.CacheWriteInputTokens ||
		next.CacheReadInputTokens != u.value.CacheReadInputTokens ||
		next.OutputTokens < u.value.OutputTokens)) {
		u.poisoned = true
	}
	if u.poisoned {
		u.value = Usage{}
	} else if next.Present {
		u.value = next
	}
	return u.value
}
