package openai

import (
	"crypto/sha256"
	"crypto/subtle"
	"hash"
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
