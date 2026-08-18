package openai

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"strings"
)

// sensitiveGuard detects exact sensitive byte strings in bytes about to cross
// the response boundary without retaining the original credential. Each
// detector keeps only a cryptographic digest, length, rolling hash, and a
// response-sized sliding window. Credential inputs are cleared by the caller
// immediately after construction.
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

const rollingBase uint64 = 257

const (
	maxSemanticGuardPaths = 64
	maxSemanticJSONDepth  = 64
	maxSemanticPathBytes  = 4096
)

var errSemanticResponseRejected = errors.New("openai connector: semantic response rejected")

func newSensitiveGuard(materials ...[]byte) *sensitiveGuard {
	guard := &sensitiveGuard{detectors: make([]*rollingDetector, 0, len(materials))}
	for _, material := range materials {
		if len(material) == 0 {
			continue
		}
		detector := &rollingDetector{
			length: len(material),
			digest: sha256.Sum256(material),
			window: make([]byte, len(material)),
			hasher: sha256.New(),
			power:  1,
		}
		for i, b := range material {
			detector.patternHash = detector.patternHash*rollingBase + uint64(b)
			if i+1 < len(material) {
				detector.power *= rollingBase
			}
		}
		guard.detectors = append(guard.detectors, detector)
	}
	return guard
}

// Contains reports whether the concatenation of all bytes fed so far contains
// a complete sensitive pattern. Callers feed exactly the bytes they are about
// to write, so a match spanning response writes is detected too.
func (g *sensitiveGuard) Contains(data []byte) bool {
	if g == nil || g.matched {
		return g != nil && g.matched
	}
	for _, b := range data {
		for _, detector := range g.detectors {
			if detector.push(b) {
				g.matched = true
				return true
			}
		}
	}
	return false
}

// clone returns a fresh rolling state backed only by the source guard's
// lengths and fingerprints. It never reconstructs or retains the original
// sensitive material.
func (g *sensitiveGuard) clone() *sensitiveGuard {
	cloned := &sensitiveGuard{}
	if g == nil {
		return cloned
	}
	cloned.detectors = make([]*rollingDetector, 0, len(g.detectors))
	for _, detector := range g.detectors {
		cloned.detectors = append(cloned.detectors, &rollingDetector{
			length:      detector.length,
			patternHash: detector.patternHash,
			power:       detector.power,
			digest:      detector.digest,
			window:      make([]byte, detector.length),
			hasher:      sha256.New(),
		})
	}
	return cloned
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

// responseGuard protects both the literal response wire and decoded JSON
// string channels. Literal scanning catches an ordinary reflection, including
// one split across writes. Per-path semantic scanning additionally catches a
// credential split across successive OpenAI chunks: JSON framing separates
// the bytes on the wire, but clients concatenate values such as
// choices[].delta.content.
type responseGuard struct {
	wire     *sensitiveGuard
	template *sensitiveGuard
	paths    map[string]*sensitiveGuard
	matched  bool
}

func newResponseGuard(materials ...[]byte) *responseGuard {
	return &responseGuard{
		wire:     newSensitiveGuard(materials...),
		template: newSensitiveGuard(materials...),
		paths:    make(map[string]*sensitiveGuard),
	}
}

func (g *responseGuard) ContainsBytes(data []byte) bool {
	if g == nil || g.matched {
		return g != nil && g.matched
	}
	if g.wire.Contains(data) {
		g.matched = true
	}
	return g.matched
}

// ContainsJSON scans the exact bytes about to cross the wire and the decoded
// JSON string values. Array positions are normalized in semantic paths so
// successive choice/content blocks share one rolling detector. Any malformed,
// excessively deep, or path-explosive JSON fails closed; protocol validation
// independently enforces the accepted OpenAI response shape.
func (g *responseGuard) ContainsJSON(wire, jsonData []byte) bool {
	if g == nil || g.matched {
		return g != nil && g.matched
	}
	if g.ContainsBytes(wire) {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.UseNumber()
	if err := g.scanJSONValue(decoder, "", 0); err != nil {
		g.matched = true
		return true
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		g.matched = true
	}
	return g.matched
}

func (g *responseGuard) scanJSONValue(decoder *json.Decoder, path string, depth int) error {
	if depth > maxSemanticJSONDepth {
		return errSemanticResponseRejected
	}
	token, err := decoder.Token()
	if err != nil {
		return errSemanticResponseRejected
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		value, isString := token.(string)
		if !isString || value == "" {
			return nil
		}
		guard, err := g.guardForPath(path)
		if err != nil {
			return err
		}
		decoded := []byte(value)
		matched := guard.Contains(decoded)
		clear(decoded)
		if matched {
			return errSemanticResponseRejected
		}
		return nil
	}

	switch delimiter {
	case '{':
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return errSemanticResponseRejected
			}
			key, ok := keyToken.(string)
			if !ok {
				return errSemanticResponseRejected
			}
			childPath := path + "/" + escapeSemanticPath(key)
			if len(childPath) > maxSemanticPathBytes {
				return errSemanticResponseRejected
			}
			if err := g.scanJSONValue(decoder, childPath, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errSemanticResponseRejected
		}
	case '[':
		childPath := path + "/[]"
		if len(childPath) > maxSemanticPathBytes {
			return errSemanticResponseRejected
		}
		for decoder.More() {
			if err := g.scanJSONValue(decoder, childPath, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errSemanticResponseRejected
		}
	default:
		return errSemanticResponseRejected
	}
	return nil
}

func escapeSemanticPath(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func (g *responseGuard) guardForPath(path string) (*sensitiveGuard, error) {
	if guard, ok := g.paths[path]; ok {
		return guard, nil
	}
	if len(g.paths) >= maxSemanticGuardPaths {
		return nil, errSemanticResponseRejected
	}
	guard := g.template.clone()
	g.paths[path] = guard
	return guard, nil
}

func (g *responseGuard) Clear() {
	if g == nil {
		return
	}
	g.wire.Clear()
	g.template.Clear()
	for path, guard := range g.paths {
		guard.Clear()
		delete(g.paths, path)
	}
	g.paths = nil
	g.matched = false
}
