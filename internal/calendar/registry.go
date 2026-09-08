package calendar

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const zoneVersion = "go1.26.6-zoneinfo"
const zoneArchiveSHA256 = "8f55634d05f8bca1f7bc7c69c5933428c69357e0bdf565e5ba224e3f88ff12e8"

//go:embed zoneinfo.zip
var zoneinfoZip []byte

type registeredZone struct {
	location *time.Location
	offsets  []int
}

type zoneRegistry struct {
	names []string
	zones map[string]registeredZone
}

var embeddedRegistry = sync.OnceValue(readRegistry)

func ZoneVersion() string { return zoneVersion }

// Zones returns an independent sorted slice, including IANA compatibility
// names supplied in the archive. Local and arbitrary file paths are excluded.
func Zones() []string { return slices.Clone(embeddedRegistry().names) }

func ValidZone(name string) bool {
	_, ok := embeddedRegistry().zones[name]
	return ok
}

func lookupZone(name string) (registeredZone, error) {
	zone, ok := embeddedRegistry().zones[name]
	if !ok {
		return registeredZone{}, ErrInvalidTime
	}
	return zone, nil
}

func readRegistry() zoneRegistry {
	digest := sha256.Sum256(zoneinfoZip)
	if hex.EncodeToString(digest[:]) != zoneArchiveSHA256 {
		panic("calendar: embedded zone archive digest mismatch")
	}
	archive, err := zip.NewReader(bytes.NewReader(zoneinfoZip), int64(len(zoneinfoZip)))
	if err != nil {
		panic("calendar: invalid embedded zone archive")
	}
	registry := zoneRegistry{zones: map[string]registeredZone{"UTC": {time.UTC, []int{0}}}}
	for _, file := range archive.File {
		name := file.Name
		if name == "Local" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.HasSuffix(name, "/") {
			panic("calendar: invalid embedded zone name")
		}
		reader, err := file.Open()
		if err != nil {
			panic("calendar: unreadable embedded zone")
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, 1<<20))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || len(data) == 1<<20 {
			panic("calendar: invalid embedded zone data")
		}
		location, err := time.LoadLocationFromTZData(name, data)
		if err != nil {
			panic("calendar: invalid embedded TZif")
		}
		offsets, ok := tzifOffsets(data)
		if !ok {
			panic("calendar: invalid embedded offsets")
		}
		registry.zones[name] = registeredZone{location, offsets}
	}
	for name := range registry.zones {
		registry.names = append(registry.names, name)
	}
	slices.Sort(registry.names)
	return registry
}

// Read numeric offsets from TZif type records and the POSIX continuation.
// Resolution still uses time.Location for every round trip and transition;
// this reader does not reimplement the IANA transition rules.
func tzifOffsets(data []byte) ([]int, bool) {
	width := uint64(4)
	for block := 0; block < 2; block++ {
		if len(data) < 44 || string(data[:4]) != "TZif" {
			return nil, false
		}
		var counts [6]uint64
		for i := range counts {
			counts[i] = uint64(binary.BigEndian.Uint32(data[20+i*4 : 24+i*4]))
		}
		isut, isstd, leaps, transitions, types, chars := counts[0], counts[1], counts[2], counts[3], counts[4], counts[5]
		blockSize := transitions*(width+1) + types*6 + chars + leaps*(width+4) + isstd + isut
		if types == 0 || types > 256 || blockSize > uint64(len(data)-44) {
			return nil, false
		}
		if block == 0 && data[4] != 0 {
			data = data[44+blockSize:]
			width = 8
			continue
		}
		start := uint64(44) + transitions*(width+1)
		var offsets []int
		for i := uint64(0); i < types; i++ {
			offset := int(int32(binary.BigEndian.Uint32(data[start+i*6 : start+i*6+4])))
			// Also bounds a clamped month (at most 31 days) plus both
			// endpoint offsets strictly below the 35-day retention limit.
			if offset <= -86400 || offset >= 86400 {
				return nil, false
			}
			if !slices.Contains(offsets, offset) {
				offsets = append(offsets, offset)
			}
		}
		if data[4] != 0 {
			footer := data[44+blockSize:]
			if len(footer) < 2 || footer[0] != '\n' || footer[len(footer)-1] != '\n' {
				return nil, false
			}
			extra, ok := continuationOffsets(string(footer[1 : len(footer)-1]))
			if !ok {
				return nil, false
			}
			for _, offset := range extra {
				if offset <= -86400 || offset >= 86400 {
					return nil, false
				}
				if !slices.Contains(offsets, offset) {
					offsets = append(offsets, offset)
				}
			}
		}
		slices.Sort(offsets)
		return offsets, true
	}
	return nil, false
}

// Only the names and numeric offsets are read here. time.Location remains
// responsible for the actual dates and transition rules in the suffix.
func continuationOffsets(value string) ([]int, bool) {
	if value == "" {
		return nil, true
	}
	skipName := func(value string) (string, bool) {
		if strings.HasPrefix(value, "<") {
			end := strings.IndexByte(value, '>')
			if end < 2 {
				return "", false
			}
			return value[end+1:], true
		}
		end := 0
		for end < len(value) && (value[end] >= 'A' && value[end] <= 'Z' || value[end] >= 'a' && value[end] <= 'z') {
			end++
		}
		return value[end:], end >= 3
	}
	readOffset := func(value string) (int, string, bool) {
		end := 0
		if end < len(value) && (value[end] == '+' || value[end] == '-') {
			end++
		}
		for end < len(value) && (value[end] >= '0' && value[end] <= '9' || value[end] == ':') {
			end++
		}
		text := value[:end]
		sign := -1
		if strings.HasPrefix(text, "-") {
			sign = 1
		}
		text = strings.TrimLeft(text, "+-")
		parts := strings.Split(text, ":")
		if len(parts) > 3 {
			return 0, "", false
		}
		seconds := 0
		for i, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 || i > 0 && n > 59 || i == 0 && n > 24 {
				return 0, "", false
			}
			seconds += n * []int{3600, 60, 1}[i]
		}
		return sign * seconds, value[end:], true
	}
	rest, ok := skipName(value)
	if !ok {
		return nil, false
	}
	standard, rest, ok := readOffset(rest)
	if !ok {
		return nil, false
	}
	if rest == "" {
		return []int{standard}, true
	}
	rest, ok = skipName(rest)
	if !ok {
		return nil, false
	}
	dst := standard + 3600
	if rest != "" && rest[0] != ',' {
		dst, rest, ok = readOffset(rest)
		if !ok {
			return nil, false
		}
	}
	if rest != "" && rest[0] != ',' {
		return nil, false
	}
	return []int{standard, dst}, true
}
