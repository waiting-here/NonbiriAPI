package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxJSONDepth = 16
const maxSafeInteger = 1<<53 - 1

// ParseLevel rejects ambiguous JSON before decoding the closed gameplay DTO.
func ParseLevel(raw []byte) (Level, error) {
	if len(raw) == 0 || len(raw) > MaxLevelBytes || !utf8.Valid(raw) {
		return Level{}, errors.New("level size or UTF-8 is invalid")
	}
	if err := checkJSON(raw, maxJSONDepth); err != nil {
		return Level{}, err
	}
	if err := checkLevelShape(raw); err != nil {
		return Level{}, err
	}
	var level Level
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&level); err != nil {
		return Level{}, fmt.Errorf("level shape: %w", err)
	}
	if err := ValidateLevel(level); err != nil {
		return Level{}, err
	}
	return normalizeLevel(level), nil
}

func checkJSON(raw []byte, maxDepth int) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanValue(decoder, 0, maxDepth); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func scanValue(decoder *json.Decoder, depth, maxDepth int) error {
	if depth > maxDepth {
		return errors.New("JSON depth exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return fmt.Errorf("invalid object key: %w", err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("object key must be a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				seen[key] = struct{}{}
				if err := scanValue(decoder, depth+1, maxDepth); err != nil {
					return err
				}
			}
			_, err := decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := scanValue(decoder, depth+1, maxDepth); err != nil {
					return err
				}
			}
			_, err := decoder.Token()
			return err
		default:
			return errors.New("unexpected JSON delimiter")
		}
	case json.Number:
		literal := string(value)
		if literal == "-0" || strings.ContainsAny(literal, ".eE") {
			return fmt.Errorf("non-canonical integer %q", literal)
		}
		number, err := strconv.ParseInt(literal, 10, 64)
		if err != nil || number < -maxSafeInteger || number > maxSafeInteger {
			return errors.New("JSON integer exceeds safe range")
		}
	}
	return nil
}

func normalizePolygon(polygon Polygon) Polygon {
	polygon.Holes = slices.Clone(polygon.Holes)
	if polygon.Outer == nil {
		polygon.Outer = []Point{}
	}
	if polygon.Holes == nil {
		polygon.Holes = [][]Point{}
	}
	for index := range polygon.Holes {
		if polygon.Holes[index] == nil {
			polygon.Holes[index] = []Point{}
		}
	}
	return polygon
}

func normalizeLevel(level Level) Level {
	// A stored level may be replayed by more than one worker. Copy every slice
	// whose elements are normalized so canonicalization never writes through
	// the caller's immutable object graph.
	level.Tools = slices.Clone(level.Tools)
	level.Solids = slices.Clone(level.Solids)
	level.Hazards = slices.Clone(level.Hazards)
	level.Bowls = slices.Clone(level.Bowls)
	level.Switches = slices.Clone(level.Switches)
	level.Gates = slices.Clone(level.Gates)
	level.Directions = slices.Clone(level.Directions)
	if level.Fish == nil {
		level.Fish = []FishSpec{}
	}
	if level.Tools == nil {
		level.Tools = []Tool{}
	}
	if level.Solids == nil {
		level.Solids = []Shape{}
	}
	if level.Hazards == nil {
		level.Hazards = []Hazard{}
	}
	if level.Bowls == nil {
		level.Bowls = []Bowl{}
	}
	if level.Switches == nil {
		level.Switches = []Switch{}
	}
	if level.Gates == nil {
		level.Gates = []Gate{}
	}
	if level.Directions == nil {
		level.Directions = []Direction{}
	}
	for index := range level.Tools {
		level.Tools[index].Polygon = normalizePolygon(level.Tools[index].Polygon)
	}
	for index := range level.Solids {
		level.Solids[index].Polygon = normalizePolygon(level.Solids[index].Polygon)
	}
	for index := range level.Hazards {
		level.Hazards[index].Polygon = normalizePolygon(level.Hazards[index].Polygon)
	}
	for index := range level.Bowls {
		level.Bowls[index].Polygon = normalizePolygon(level.Bowls[index].Polygon)
	}
	for index := range level.Switches {
		level.Switches[index].Polygon = normalizePolygon(level.Switches[index].Polygon)
	}
	for index := range level.Gates {
		level.Gates[index].Polygon = normalizePolygon(level.Gates[index].Polygon)
		if level.Gates[index].SwitchIDs == nil {
			level.Gates[index].SwitchIDs = []int{}
		}
	}
	for index := range level.Directions {
		level.Directions[index].Polygon = normalizePolygon(level.Directions[index].Polygon)
	}
	return level
}

// CanonicalJSON sorts ASCII gameplay keys bytewise and preserves array order.
// All gameplay strings are restricted to ASCII enum tokens or fixed format IDs.
func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var data any
	if err := decoder.Decode(&data); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := writeCanonical(&output, data); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeCanonical(output *bytes.Buffer, data any) error {
	switch value := data.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			output.Write(encoded)
			output.WriteByte(':')
			if err := writeCanonical(output, value[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	case []any:
		output.WriteByte('[')
		for index, item := range value {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonical(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case json.Number:
		output.WriteString(string(value))
	case string, bool, nil:
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		output.Write(encoded)
	default:
		return fmt.Errorf("unsupported canonical value %T", data)
	}
	return nil
}

func NormalizedLevel(level Level) ([]byte, error) {
	if err := ValidateLevel(level); err != nil {
		return nil, err
	}
	return CanonicalJSON(normalizeLevel(level))
}

func ContentHash(level Level) (string, error) {
	canonical, err := NormalizedLevel(level)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func StateDigest(state EngineState) (string, error) {
	return stateDigest(state, 5000, false)
}

// StateDigestForVersion validates motion with the bound rules before hashing.
func StateDigestForVersion(version int, state EngineState) (string, error) {
	if !supportedVersions(version, ScoringVersion) {
		return "", errors.New("unsupported state rules version")
	}
	if version == EngineVersion {
		return stateDigest(state, turnDenominatorV3, true)
	}
	return StateDigest(state)
}

func stateDigest(state EngineState, denominator int64, requireMotion bool) (string, error) {
	v2 := 0
	for _, fish := range state.Fish {
		if fish.Motion != nil {
			v2++
			if fish.Motion.TurnRemainder < 0 || fish.Motion.TurnRemainder >= denominator || fish.Motion.AmbiguousTurnDir < -1 || fish.Motion.AmbiguousTurnDir > 1 || fish.TurnDistance != 0 || fish.TurnDir < -1 || fish.TurnDir > 1 {
				return "", errors.New("version 2 motion state is invalid")
			}
		}
	}
	if (v2 != 0 || requireMotion) && v2 != len(state.Fish) {
		return "", errors.New("mixed motion state versions")
	}
	canonical, err := CanonicalJSON(state)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func SeedCommit(challengeID, periodID, nodeID, contentHash string, seed [32]byte) (string, error) {
	return SeedCommitForVersion(challengeID, periodID, nodeID, contentHash, LegacyEngineVersion, ScoringVersion, seed)
}

func supportedVersions(engineVersion, scoringVersion int) bool {
	return (engineVersion == LegacyEngineVersion || engineVersion == PreviousEngineVersion || engineVersion == EngineVersion) && scoringVersion == ScoringVersion
}

// SeedCommitForVersion binds the actual immutable rules versions of a level.
func SeedCommitForVersion(challengeID, periodID, nodeID, contentHash string, engineVersion, scoringVersion int, seed [32]byte) (string, error) {
	if !supportedVersions(engineVersion, scoringVersion) {
		return "", errors.New("commit rules version is unsupported")
	}
	if !canonicalID(challengeID) || !canonicalID(periodID) || !canonicalID(nodeID) {
		return "", errors.New("commit ID is invalid")
	}
	content, err := hex.DecodeString(contentHash)
	if err != nil || len(content) != 32 || strings.ToLower(contentHash) != contentHash {
		return "", errors.New("content hash is invalid")
	}
	var payload bytes.Buffer
	for _, part := range [][]byte{
		[]byte("nonbiri-fatfish-commit-v1"), []byte(challengeID), []byte(periodID), []byte(nodeID),
		content, []byte(strconv.Itoa(engineVersion)), []byte(strconv.Itoa(scoringVersion)), seed[:],
	} {
		if uint64(len(part)) > uint64(^uint32(0)) {
			return "", errors.New("commit field is too large")
		}
		if err := binary.Write(&payload, binary.BigEndian, uint32(len(part))); err != nil {
			return "", err
		}
		payload.Write(part)
	}
	digest := sha256.Sum256(payload.Bytes())
	return hex.EncodeToString(digest[:]), nil
}

func canonicalID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			if char < 'A' || char > 'Z' {
				if char < 'a' || char > 'z' {
					if char != '-' && char != '_' {
						return false
					}
				}
			}
		}
	}
	return true
}
