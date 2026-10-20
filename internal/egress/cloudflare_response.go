package egress

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

var errRouteReflection = errors.New("routed upstream response contains private authentication data")

// Prefix-state accounting charges the fixed path key/map slot and retained
// bytes against the consumer's existing event budget. It cannot grow for the
// lifetime of a long stream merely because new extension fields arrive.
type routeGuardStateTooLargeError struct{ Limit int64 }

func (e *routeGuardStateTooLargeError) Error() string {
	return fmt.Sprintf("routed response inspection exceeds its %d-byte state limit", e.Limit)
}

// Values stay private to one response and never appear through formatting.
// Per-path suffixes cover a secret split between consecutive SSE JSON deltas.
type routeSecrets struct {
	values     []string
	tails      map[[sha256.Size]byte]string
	stateLimit int64
	stateBytes int64
	failure    error
}

func (*routeSecrets) String() string     { return "[private route response guard]" }
func (s *routeSecrets) GoString() string { return s.String() }
func newRouteSecrets(values ...string) *routeSecrets {
	s := &routeSecrets{tails: make(map[[sha256.Size]byte]string), stateLimit: DefaultMaxResponseBytes}
	for _, value := range values {
		s.add(value)
	}
	return s
}
func (s *routeSecrets) add(value string) {
	if value == "" || value == "/" {
		return
	}
	for _, existing := range s.values {
		if existing == value {
			return
		}
	}
	s.values = append(s.values, value)
}
func (s *routeSecrets) containsRaw(value string) bool {
	for _, secret := range s.values {
		if strings.Contains(value, secret) || strings.Contains(value, url.QueryEscape(secret)) || strings.Contains(value, url.PathEscape(secret)) {
			return true
		}
	}
	return false
}
func (s *routeSecrets) contains(value string) bool {
	for range 3 {
		if s.containsRaw(value) {
			return true
		}
		value = decodeJSONEscapes(html.UnescapeString(value))
		if s.containsRaw(value) {
			return true
		}
		query, err := url.QueryUnescape(value)
		if err == nil && s.containsRaw(query) {
			return true
		}
		path, err := url.PathUnescape(value)
		if err == nil {
			value = path
		}
	}
	return false
}
func (s *routeSecrets) cleanHeaders(headers http.Header) {
	for name, values := range headers {
		if s.contains(name) {
			delete(headers, name)
			continue
		}
		for i, value := range values {
			if s.contains(value) {
				values[i] = "[redacted]"
			}
		}
	}
}
func decodeJSONEscapes(value string) string {
	if !strings.Contains(value, "\\") {
		return value
	}
	var output strings.Builder
	for i := 0; i < len(value); {
		size := 2
		if value[i] == 92 && i+1 < len(value) && value[i+1] == 'u' {
			size = 6
			if i+12 <= len(value) && value[i+6] == 92 && value[i+7] == 'u' {
				size = 12
			}
		}
		if value[i] == 92 && i+size <= len(value) {
			var decoded string
			if json.Unmarshal([]byte("\""+value[i:i+size]+"\""), &decoded) == nil {
				output.WriteString(decoded)
				i += size
				continue
			}
		}
		output.WriteByte(value[i])
		i++
	}
	return output.String()
}
func (s *routeSecrets) semantic(path [sha256.Size]byte, value string) bool {
	combined := s.tails[path] + value
	if s.contains(combined) {
		return true
	}
	// Harmless fields need no retained state. Only a suffix that could become
	// private material in a later event keeps a bounded rolling candidate.
	candidate := combined
	prefixBytes := 0
	partialBytes := 0
	for range 3 {
		partial := 0
		if strings.HasSuffix(candidate, "%") {
			partial = 1
		} else if len(candidate) > 1 && candidate[len(candidate)-2] == '%' {
			partial = 2
		}
		partialBytes = max(partialBytes, partial)
		candidate = candidate[:len(candidate)-partial]
		for _, secret := range s.values {
			for _, pattern := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
				for length := min(len(pattern)-1, len(candidate)); length > 0; length-- {
					if strings.HasPrefix(pattern, candidate[len(candidate)-length:]) {
						prefixBytes = max(prefixBytes, length)
						break
					}
				}
			}
		}
		decoded, err := url.PathUnescape(candidate)
		if err == nil {
			candidate = decoded
		}
		candidate = decodeJSONEscapes(html.UnescapeString(candidate))
	}
	if prefixBytes > 0 || partialBytes > 0 {
		size := min(len(combined), prefixBytes*9+partialBytes)
		old, exists := s.tails[path]
		charge := int64(size - len(old))
		if !exists {
			charge += 64
		}
		if charge > s.stateLimit-s.stateBytes {
			s.failure = &routeGuardStateTooLargeError{Limit: s.stateLimit}
			return true
		}
		s.stateBytes += charge
		s.tails[path] = combined[len(combined)-size:]
	} else {
		if old, exists := s.tails[path]; exists {
			s.stateBytes -= int64(len(old)) + 64
			delete(s.tails, path)
		}
	}
	return false
}
func routeJSONPath(parent [sha256.Size]byte, name string, array bool) [sha256.Size]byte {
	hash := sha256.New()
	hash.Write(parent[:])
	var prefix [9]byte
	if array {
		prefix[0] = 1
	}
	binary.BigEndian.PutUint64(prefix[1:], uint64(len(name)))
	hash.Write(prefix[:])
	hash.Write([]byte(name))
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}
func (s *routeSecrets) inspectJSON(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var scan func([sha256.Size]byte, int) bool
	scan = func(path [sha256.Size]byte, depth int) bool {
		if depth > 10000 {
			return true
		}
		token, err := decoder.Token()
		if err != nil {
			return true
		}
		delim, ok := token.(json.Delim)
		if !ok {
			value, ok := token.(string)
			return ok && s.semantic(path, value)
		}
		switch delim {
		case '{':
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return true
				}
				name, ok := key.(string)
				if !ok || s.contains(name) {
					return true
				}
				if scan(routeJSONPath(path, name, false), depth+1) {
					return true
				}
			}
			end, err := decoder.Token()
			return err != nil || end != json.Delim('}')
		case '[':
			for decoder.More() {
				if scan(routeJSONPath(path, "", true), depth+1) {
					return true
				}
			}
			end, err := decoder.Token()
			return err != nil || end != json.Delim(']')
		default:
			return true
		}
	}
	if scan([sha256.Size]byte{}, 0) {
		return true
	}
	_, err := decoder.Token()
	return !errors.Is(err, io.EOF)
}

// Ordinary responses are already finite; streams are inspected one event at
// a time without buffering their lifetime or creating a second read goroutine.
type routeResponseBody struct {
	source    io.ReadCloser
	response  *http.Response
	reader    *bufio.Reader
	secrets   *routeSecrets
	limits    ResponseLimits
	streaming bool
	pending   []byte
	terminal  error
	firstLine bool
}

func newRouteResponseBody(source io.ReadCloser, response *http.Response, secrets *routeSecrets, limits ResponseLimits) *routeResponseBody {
	secrets.stateLimit = limits.JSONBytes
	if routeResponseIsSSE(response) {
		secrets.stateLimit = int64(limits.EventBytes)
	}
	return &routeResponseBody{source: source, response: response, reader: bufio.NewReaderSize(source, min(defaultSSEReadBuffer, limits.LineBytes)), secrets: secrets, limits: limits, streaming: routeResponseIsSSE(response), firstLine: true}
}
func routeResponseIsSSE(response *http.Response) bool {
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	return media == "text/event-stream"
}
func (b *routeResponseBody) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if len(b.pending) == 0 && b.terminal == nil {
		b.fill()
	}
	if len(b.pending) != 0 {
		n := copy(dst, b.pending)
		clear(b.pending[:n])
		b.pending = b.pending[n:]
		return n, nil
	}
	return 0, b.terminal
}
func (b *routeResponseBody) Close() error { return safeRouteError(b.source.Close()) }
func (b *routeResponseBody) fill() {
	var data []byte
	var err error
	if b.streaming {
		data, err = b.readEvent()
	} else {
		data, err = io.ReadAll(b.reader)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		clear(data)
		b.terminal = safeRouteError(err)
		_ = b.Close()
		return
	}
	rejected := b.secrets.contains(string(data))
	if !rejected {
		if b.streaming {
			var fields []string
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSuffix(line, "\r")
				if value, ok := strings.CutPrefix(line, "data:"); ok {
					fields = append(fields, strings.TrimPrefix(value, " "))
				}
			}
			payload := []byte(strings.Join(fields, "\n"))
			if bytes.HasPrefix(bytes.TrimSpace(payload), []byte("{")) || bytes.HasPrefix(bytes.TrimSpace(payload), []byte("[")) && !bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
				rejected = b.secrets.inspectJSON(payload)
			}
		} else if json.Valid(data) || bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) || bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
			rejected = b.secrets.inspectJSON(data)
		}
	}
	if rejected {
		clear(data)
		b.terminal = errRouteReflection
		if b.secrets.failure != nil {
			b.terminal = b.secrets.failure
		}
		_ = b.Close()
		return
	}
	b.pending = data
	if err != nil || !b.streaming {
		b.terminal = io.EOF
		b.secrets.cleanHeaders(b.response.Trailer)
	}
}
func (b *routeResponseBody) readEvent() ([]byte, error) {
	var event []byte
	payloadBytes := 0
	dataLines := 0
	for {
		line, eof, err := readBoundedLine(b.reader, b.limits.LineBytes)
		if err != nil {
			return nil, err
		}
		if b.firstLine {
			line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
			b.firstLine = false
		}
		if len(line) == 0 {
			if len(event) > 0 {
				event = append(event, 10)
				if eof {
					return event, io.EOF
				}
				return event, nil
			}
			if eof {
				return nil, io.EOF
			}
			continue
		}
		// Comments and unknown fields have no SSE semantics. Do not accumulate
		// them while waiting for an event, just as the existing SSE parser does.
		name, value, found := bytes.Cut(line, []byte(":"))
		if !found {
			value = nil
		}
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(name) {
		case "data":
			extra := len(value)
			if dataLines > 0 {
				extra++
			}
			if extra > b.limits.EventBytes-payloadBytes {
				return nil, &SSEEventTooLargeError{Limit: b.limits.EventBytes}
			}
			payloadBytes += extra
			dataLines++
		case "event", "id", "retry":
			// Keep the latest metadata value only, bounding repeated metadata lines.
			prefix := string(name) + ":"
			lines := bytes.Split(event, []byte{10})
			event = nil
			for _, existing := range lines {
				if len(existing) > 0 && !bytes.HasPrefix(existing, []byte(prefix)) {
					event = append(event, existing...)
					event = append(event, 10)
				}
			}
		default:
			if eof {
				return event, io.EOF
			}
			continue
		}
		event = append(event, line...)
		event = append(event, 10)
		if eof {
			return event, io.EOF
		}
	}
}
