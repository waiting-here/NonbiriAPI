// Package randomness creates independent game commitments and reproducible,
// cryptographic random draws. It is never used for authentication or encryption.
package randomness

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"
)

const Algorithm = "hmac-sha256-reject64-v1"

// Fishing's existing decoration budget is 65,536 draws, plus at most 40
// economic draws in a ten-catch batch. Other games need fewer samples.
const MaxSamples = 65664
const MaxStreams = 1024
const MaxBytes = 2 << 20

var ErrInvalid = errors.New("game randomness: invalid proof")
var ErrLimit = errors.New("game randomness: proof limit exceeded")

type StreamProof struct {
	Label string `json:"label"`
	// Each sample is an unsigned big-endian (bound, result) pair, 16 bytes.
	Samples string `json:"samples"`
}

// Proof contains no seed, draws or usage information until the owning game
// authorizes terminal disclosure. Zero-value proofs represent older games.
type Proof struct {
	Algorithm  string        `json:"algorithm"`
	Game       string        `json:"game"`
	ResourceID string        `json:"resource_id"`
	Rules      string        `json:"rules"`
	Commitment string        `json:"commitment"`
	Seed       string        `json:"seed,omitempty"`
	Streams    []StreamProof `json:"streams,omitempty"`
}

// Secret has no exported fields and cannot accidentally serialize its seed.
type Secret struct {
	game, resource, rules string
	seed                  [32]byte
	streams               []*Stream
	total                 int
}

type Stream struct {
	secret  *Secret
	label   string
	counter uint64
	raw     []byte
}

func validText(value string, max int) bool {
	if len(value) < 1 || len(value) > max {
		return false
	}
	for _, c := range []byte(value) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func validGame(game string) bool {
	switch game {
	case "fishing", "linklink", "rps", "bidding", "likes", "blackjack":
		return true
	}
	return false
}

func New(game, resource, rules string, entropy io.Reader) (*Secret, error) {
	if !validGame(game) || !validText(resource, 64) || !validText(rules, 128) {
		return nil, ErrInvalid
	}
	if entropy == nil {
		entropy = rand.Reader
	}
	s := &Secret{game: game, resource: resource, rules: rules}
	if _, err := io.ReadFull(entropy, s.seed[:]); err != nil {
		return nil, errors.New("game randomness: entropy unavailable")
	}
	return s, nil
}

func (s *Secret) domain(kind string) []byte {
	return []byte("nonbiri/game-random/" + Algorithm + "\x00" + kind + "\x00" + s.game + "\x00" + s.resource + "\x00" + s.rules + "\x00")
}

func (s *Secret) Commitment() string {
	h := sha256.New()
	_, _ = h.Write(s.domain("commit"))
	_, _ = h.Write(s.seed[:])
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Secret) Stream(label string) (*Stream, error) {
	if s == nil || !validText(label, 96) {
		return nil, ErrInvalid
	}
	for _, stream := range s.streams {
		if stream.label == label {
			return stream, nil
		}
	}
	if len(s.streams) >= MaxStreams {
		return nil, ErrLimit
	}
	stream := &Stream{secret: s, label: label}
	s.streams = append(s.streams, stream)
	return stream, nil
}

// Indexed derives a single reproducible value without growing a transcript.
// The owning rules must define its stable label and bound in public inputs.
// Its separate domain cannot overlap a recorded stream of the same name.
func (s *Secret) Indexed(label string, bound uint64) (uint64, error) {
	if s == nil || !validText(label, 96) {
		return 0, ErrInvalid
	}
	stream := &Stream{secret: s, label: label}
	return stream.draw("indexed", bound)
}

func (s *Stream) draw(kind string, bound uint64) (uint64, error) {
	if s == nil || s.secret == nil || bound == 0 {
		return 0, ErrInvalid
	}
	// Reject the incomplete residue interval of 2^64 before taking modulo.
	threshold := -bound % bound
	for range 128 {
		if s.counter == ^uint64(0) {
			return 0, ErrLimit
		}
		mac := hmac.New(sha256.New, s.secret.seed[:])
		_, _ = mac.Write(s.secret.domain(kind))
		_, _ = mac.Write([]byte(s.label + "\x00"))
		var counter [8]byte
		binary.BigEndian.PutUint64(counter[:], s.counter)
		s.counter++
		_, _ = mac.Write(counter[:])
		value := binary.BigEndian.Uint64(mac.Sum(nil)[:8])
		if value >= threshold {
			return value % bound, nil
		}
	}
	return 0, ErrLimit
}

func (s *Stream) Uint64n(bound uint64) (uint64, error) {
	if s == nil || s.secret == nil {
		return 0, ErrInvalid
	}
	if s.secret.total >= MaxSamples {
		return 0, ErrLimit
	}
	index, err := s.draw("draw", bound)
	if err != nil {
		return 0, err
	}
	var pair [16]byte
	binary.BigEndian.PutUint64(pair[:8], bound)
	binary.BigEndian.PutUint64(pair[8:], index)
	s.raw = append(s.raw, pair[:]...)
	s.secret.total++
	return index, nil
}

// Read supports existing engine injection seams. Game engines use Index to
// record the actual bound in one sample instead of consuming individual bytes.
func (s *Stream) Read(p []byte) (int, error) {
	for i := range p {
		v, err := s.Uint64n(256)
		if err != nil {
			return i, err
		}
		p[i] = byte(v)
	}
	return len(p), nil
}

// Index retains existing io.Reader fixtures while production streams record
// each bounded draw. Both paths use rejection sampling without modulo bias.
func Index(reader io.Reader, bound uint64) (uint64, error) {
	if bound == 0 {
		return 0, ErrInvalid
	}
	if source, ok := reader.(interface{ Uint64n(uint64) (uint64, error) }); ok {
		return source.Uint64n(bound)
	}
	if reader == nil {
		reader = rand.Reader
	}
	v, err := rand.Int(reader, new(big.Int).SetUint64(bound))
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

func (s *Secret) Public(terminal bool) Proof {
	if s == nil {
		return Proof{}
	}
	p := Proof{Algorithm: Algorithm, Game: s.game, ResourceID: s.resource, Rules: s.rules, Commitment: s.Commitment()}
	if terminal {
		p.Seed = hex.EncodeToString(s.seed[:])
		p.Streams = make([]StreamProof, 0, len(s.streams))
		for _, stream := range s.streams {
			p.Streams = append(p.Streams, StreamProof{Label: stream.label, Samples: base64.StdEncoding.EncodeToString(stream.raw)})
		}
	}
	return p
}

// EncodePrivate is only for the private database column, never an HTTP DTO.
func (s *Secret) EncodePrivate() ([]byte, error) {
	if s == nil {
		return nil, ErrInvalid
	}
	body, err := json.Marshal(s.Public(true))
	if err != nil || len(body) > MaxBytes {
		return nil, ErrLimit
	}
	return body, nil
}

func DecodePrivate(body []byte) (*Secret, error) {
	if len(body) == 0 || len(body) > MaxBytes {
		return nil, ErrInvalid
	}
	var proof Proof
	d := json.NewDecoder(strings.NewReader(string(body)))
	d.DisallowUnknownFields()
	if d.Decode(&proof) != nil {
		return nil, ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrInvalid
	}
	return restore(proof)
}

// Verify checks the opening commitment and regenerates the supplied samples.
// The game rules and public action log must separately establish which draws
// were required; a transcript alone does not authenticate omitted samples.
func Verify(proof Proof) error {
	_, err := restore(proof)
	return err
}

func restore(proof Proof) (*Secret, error) {
	seed, err := hex.DecodeString(proof.Seed)
	if err != nil || len(seed) != 32 || hex.EncodeToString(seed) != proof.Seed || proof.Algorithm != Algorithm || len(proof.Streams) > MaxStreams {
		return nil, ErrInvalid
	}
	s, err := New(proof.Game, proof.ResourceID, proof.Rules, strings.NewReader(string(seed)))
	if err != nil || !hmac.Equal([]byte(s.Commitment()), []byte(proof.Commitment)) {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, record := range proof.Streams {
		if seen[record.Label] || !validText(record.Label, 96) {
			return nil, ErrInvalid
		}
		seen[record.Label] = true
		raw, err := base64.StdEncoding.Strict().DecodeString(record.Samples)
		if err != nil || len(raw)%16 != 0 || base64.StdEncoding.EncodeToString(raw) != record.Samples || s.total+len(raw)/16 > MaxSamples {
			return nil, ErrInvalid
		}
		stream, err := s.Stream(record.Label)
		if err != nil {
			return nil, err
		}
		for i := 0; i < len(raw); i += 16 {
			bound, want := binary.BigEndian.Uint64(raw[i:i+8]), binary.BigEndian.Uint64(raw[i+8:i+16])
			got, err := stream.Uint64n(bound)
			if err != nil || got != want {
				return nil, ErrInvalid
			}
		}
	}
	return s, nil
}
