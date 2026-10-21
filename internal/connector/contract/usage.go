package contract

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

const (
	ModalityText = iota
	ModalityImage
	ModalityAudio
	ModalityVideo
)

const (
	ModalityMissing          = "missing_details"
	ModalityInvalid          = "invalid_details"
	ModalityIncomplete       = "incomplete_details"
	ModalityUnknown          = "unknown_modality"
	ModalityCacheUnknown     = "cache_details_missing"
	ModalityMismatch         = "total_mismatch"
	ModalityReasoningUnknown = "reasoning_details_missing"
)

// ModalityUsage contains a complete partition only when its side is present.
// Input rows are uncached, cache write and cache read. Columns are text,
// image, audio and video. Fixed arrays keep Usage value-comparable.
type ModalityUsage struct {
	Input         [3][4]int64
	Output        [4]int64
	InputPresent  bool
	OutputPresent bool
	InputReason   string
	OutputReason  string
}

// ParseSearchUnits accepts exact JSON numbers representable in millionths.
// It never rounds a provider's count or converts it through floating point.
func ParseSearchUnits(raw []byte) (int64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 128 || raw[0] == '-' || raw[0] == '"' || !json.Valid(raw) {
		return 0, false
	}
	s := string(raw)
	exponent := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		exponent, err = strconv.Atoi(s[i+1:])
		if err != nil || exponent < -128 || exponent > 128 {
			return 0, false
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		exponent -= len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 {
		return 0, false
	}
	exponent += 6
	if exponent >= 0 {
		n.Mul(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil))
	} else {
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-exponent)), nil), r)
		if r.Sign() != 0 {
			return 0, false
		}
		n = q
	}
	return n.Int64(), n.IsInt64()
}
