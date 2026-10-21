// Package sessionheader manages temporary upstream correlation headers.
package sessionheader

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

const MaxHeaders = 16

var ErrInvalid = errors.New("session headers: invalid configuration")

type Config struct {
	Name        string `json:"name"`
	IdleSeconds int    `json:"idle_seconds"`
	Format      string `json:"format"`
}

// UnmarshalJSON applies wire defaults while rejecting explicit invalid values.
func (c *Config) UnmarshalJSON(data []byte) error {
	type raw Config
	value := raw{IdleSeconds: 300, Format: "uuid_v4"}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	for _, v := range fields {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return ErrInvalid
		}
	}
	if value.Name == "" || value.IdleSeconds < 0 || value.IdleSeconds > 86400 || !validFormat(value.Format) {
		return ErrInvalid
	}
	*c = Config(value)
	return nil
}
func Normalize(config []Config) ([]Config, error) {
	if len(config) > MaxHeaders {
		return nil, ErrInvalid
	}
	out := make([]Config, 0, len(config))
	seen := map[string]bool{}
	for _, v := range config {
		v.Name = strings.ToLower(strings.TrimSpace(v.Name))
		if !requestadaptation.ValidManagedHeaderName(v.Name) || seen[v.Name] {
			return nil, ErrInvalid
		}
		if v.Format == "" {
			v.Format = "uuid_v4"
		}
		if v.IdleSeconds < 0 || v.IdleSeconds > 86400 || !validFormat(v.Format) {
			return nil, ErrInvalid
		}
		seen[v.Name] = true
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func Encode(config []Config) (string, error) {
	normalized, err := Normalize(config)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(normalized)
	return string(b), err
}
func Decode(encoded string) ([]Config, error) {
	var v []Config
	if json.Unmarshal([]byte(encoded), &v) != nil {
		return nil, ErrInvalid
	}
	return Normalize(v)
}
func validFormat(format string) bool {
	switch format {
	case "uuid_v4", "hex_32", "opencode_session", "opencode_message", "opencode_part":
		return true
	default:
		return false
	}
}

// The timestamp and counter are shared across all three OpenCode ID kinds.
var openCodeSequence struct {
	sync.Mutex
	timestamp int64
	counter   uint64
}

func openCodeValue(format string, timestamp int64) (string, error) {
	var entropy [14]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	openCodeSequence.Lock()
	if timestamp != openCodeSequence.timestamp {
		openCodeSequence.timestamp = timestamp
		openCodeSequence.counter = 0
	}
	openCodeSequence.counter++
	current := uint64(timestamp)*0x1000 + openCodeSequence.counter
	openCodeSequence.Unlock()
	prefix := "msg_"
	if format == "opencode_session" {
		prefix = "ses_"
		current = ^current
	} else if format == "opencode_part" {
		prefix = "prt_"
	}
	var timeBytes [6]byte
	for i := 5; i >= 0; i-- {
		timeBytes[i] = byte(current)
		current >>= 8
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for i, b := range entropy {
		entropy[i] = alphabet[int(b)%len(alphabet)]
	}
	return prefix + hex.EncodeToString(timeBytes[:]) + string(entropy[:]), nil
}

func randomValue(format string) (string, error) {
	if !validFormat(format) {
		return "", ErrInvalid
	}
	if strings.HasPrefix(format, "opencode_") {
		return openCodeValue(format, time.Now().UnixMilli())
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	if format == "hex_32" {
		return hex.EncodeToString(bytes[:]), nil
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	h := hex.EncodeToString(bytes[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

var valuePattern = regexp.MustCompile(`^(?:[0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}|(?:ses_|msg_|prt_)[0-9a-f]{12}[0-9A-Za-z]{14})$`)

// Apply runs after fixed/forwarded headers. The send callback observes written
// headers, so local refusals and unsuccessful connection setup do not refresh a session.
func Apply(request *http.Request, headers http.Header, sent func()) error {
	if len(headers) == 0 {
		return nil
	}
	if len(headers) > MaxHeaders {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for name, values := range headers {
		lower := strings.ToLower(name)
		if !requestadaptation.ValidManagedHeaderName(name) || seen[lower] || len(values) != 1 || !valuePattern.MatchString(values[0]) {
			return ErrInvalid
		}
		seen[lower] = true
	}
	for name, values := range headers {
		request.Header.Set(name, values[0])
	}
	if sent != nil {
		*request = *request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{WroteHeaders: sent}))
	}
	return nil
}
