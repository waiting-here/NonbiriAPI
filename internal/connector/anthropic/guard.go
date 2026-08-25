package anthropic

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"unicode/utf8"
)

const rollingBase uint64 = 257

// sensitiveGuard retains only fingerprints and rolling windows. It is fed
// both generated wire bytes and decoded text/tool-argument deltas so JSON
// escaping or frame boundaries cannot turn a reflected credential into a
// caller-visible value.
type sensitiveGuard struct {
	detectors []*rollingDetector
	matched   bool
}

type rollingDetector struct {
	length      int
	patternHash uint64
	power       uint64
	digest      [sha256.Size]byte
	window      []byte
	position    int
	count       int
	hash        uint64
	hasher      hash.Hash
}

func newSensitiveGuard(materials ...[]byte) *sensitiveGuard {
	guard := &sensitiveGuard{detectors: make([]*rollingDetector, 0, len(materials))}
	for _, material := range materials {
		if len(material) == 0 {
			continue
		}
		// SHA-256 is an in-memory exact-match fingerprint here, not a
		// password verifier or persisted credential hash.
		// codeql[go/weak-sensitive-data-hashing]
		detector := &rollingDetector{length: len(material), digest: sha256.Sum256(material), window: make([]byte, len(material)), hasher: sha256.New(), power: 1}
		for index, value := range material {
			detector.patternHash = detector.patternHash*rollingBase + uint64(value)
			if index+1 < len(material) {
				detector.power *= rollingBase
			}
		}
		guard.detectors = append(guard.detectors, detector)
	}
	return guard
}

func (g *sensitiveGuard) Contains(data []byte) bool {
	if g == nil || g.matched {
		return g != nil && g.matched
	}
	for _, value := range data {
		for _, detector := range g.detectors {
			if detector.push(value) {
				g.matched = true
				return true
			}
		}
	}
	return false
}

func (g *sensitiveGuard) clone() *sensitiveGuard {
	clone := &sensitiveGuard{}
	if g == nil {
		return clone
	}
	clone.detectors = make([]*rollingDetector, 0, len(g.detectors))
	for _, detector := range g.detectors {
		clone.detectors = append(clone.detectors, &rollingDetector{
			length: detector.length, patternHash: detector.patternHash, power: detector.power,
			digest: detector.digest, window: make([]byte, detector.length), hasher: sha256.New(),
		})
	}
	return clone
}

// ContainsJSONStrings validates a bounded JSON value while scanning every
// decoded object key and string value. Decoding first is essential: credentials
// reflected through quote/backslash, \uXXXX, or surrogate-pair escapes must be
// rejected just like literal plaintext.
func (g *sensitiveGuard) ContainsJSONStrings(raw []byte) (bool, error) {
	if len(raw) == 0 || !utf8.Valid(raw) || validateJSONSurrogateEscapes(raw) != nil {
		return false, ErrInvalidResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	fields := 0
	matched, err := scanSensitiveJSONValue(decoder, g, 0, &fields)
	if err != nil {
		return false, err
	}
	if matched {
		return true, nil
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return false, ErrInvalidResponse
	}
	return matched, nil
}

func scanSensitiveJSONValue(decoder *json.Decoder, guard *sensitiveGuard, depth int, fields *int) (bool, error) {
	if depth > maxJSONDepth {
		return false, ErrInvalidResponse
	}
	token, err := decoder.Token()
	if err != nil {
		return false, ErrInvalidResponse
	}
	if text, ok := token.(string); ok {
		value := []byte(text)
		matched := guard.Contains(value)
		clear(value)
		return matched, nil
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return false, nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return false, ErrInvalidResponse
			}
			if _, duplicate := seen[key]; duplicate {
				return false, ErrInvalidResponse
			}
			seen[key] = struct{}{}
			*fields++
			if *fields > maxJSONFields {
				return false, ErrInvalidResponse
			}
			keyBytes := []byte(key)
			matched := guard.Contains(keyBytes)
			clear(keyBytes)
			if matched {
				return true, nil
			}
			matched, err = scanSensitiveJSONValue(decoder, guard, depth+1, fields)
			if err != nil || matched {
				return matched, err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return false, ErrInvalidResponse
		}
	case '[':
		for decoder.More() {
			matched, err := scanSensitiveJSONValue(decoder, guard, depth+1, fields)
			if err != nil || matched {
				return matched, err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return false, ErrInvalidResponse
		}
	default:
		return false, ErrInvalidResponse
	}
	return false, nil
}

func (d *rollingDetector) push(value byte) bool {
	if d.count < d.length {
		d.window[d.position] = value
		d.position = (d.position + 1) % d.length
		d.count++
		d.hash = d.hash*rollingBase + uint64(value)
	} else {
		oldest := d.window[d.position]
		d.window[d.position] = value
		d.position = (d.position + 1) % d.length
		d.hash = (d.hash-uint64(oldest)*d.power)*rollingBase + uint64(value)
	}
	if d.count != d.length || d.hash != d.patternHash {
		return false
	}
	d.hasher.Reset()
	_, _ = d.hasher.Write(d.window[d.position:])
	_, _ = d.hasher.Write(d.window[:d.position])
	sum := d.hasher.Sum(nil)
	matched := subtle.ConstantTimeCompare(sum, d.digest[:]) == 1
	clear(sum)
	return matched
}

func (g *sensitiveGuard) Clear() {
	if g == nil {
		return
	}
	for _, detector := range g.detectors {
		clear(detector.window)
		detector.window = nil
		detector.hasher.Reset()
	}
	g.detectors = nil
	g.matched = false
}
