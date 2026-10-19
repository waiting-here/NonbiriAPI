// Package headerfacts freezes credential-redacted HTTP header diagnostics.
// Private encoded facts never participate in ordinary connector observations.
package headerfacts

import (
	"bytes"
	"compress/gzip"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/textprivacy"
)

const MaxHeaderBytes = 1 << 20

// Length-prefixed bytes avoid JSON expansion for numerous tiny values.
const MaxEncodedBytes = 4 << 20

var compressionWriters = sync.Pool{New: func() any { writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed); return writer }}

type Fact struct {
	state        string
	capturedAt   int64
	status       int
	data         []byte
	decodedBytes int
}

func (Fact) String() string               { return "[private header facts]" }
func (Fact) GoString() string             { return "[private header facts]" }
func (Fact) LogValue() slog.Value         { return slog.StringValue("[private header facts]") }
func (Fact) MarshalJSON() ([]byte, error) { return []byte(`"[private header facts]"`), nil }
func (f Fact) State() string              { return f.state }
func (f Fact) CapturedAt() int64          { return f.capturedAt }
func (f Fact) Status() int                { return f.status }

// Value binds immutable compressed bytes directly; database drivers must not
// modify parameters. This avoids copying large collections inside a transaction.
func (f Fact) Value() (driver.Value, error) {
	if f.data == nil {
		return nil, nil
	}
	return f.data, nil
}
func (f Fact) BytesSaved() int    { return len(f.data) }
func (f Fact) Compressed() []byte { return bytes.Clone(f.data) }

// CredentialName is shared by inbound and upstream capture. Metadata such as
// rate-limit token counts is retained; explicit authentication material is not.
func CredentialName(name string) bool {
	n := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	switch n {
	case "authorization", "proxy-authorization", "cookie", "set-cookie",
		"authentication-info", "proxy-authentication-info", "api-key", "apikey",
		"x-api-key", "x-apikey", "x-goog-api-key", "x-auth", "x-authorization":
		return true
	}
	for _, suffix := range []string{"-api-key", "-apikey", "-token", "-secret", "-password", "-credential", "-credentials"} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return n == "token" || n == "secret" || n == "password" || n == "credential" || n == "credentials"
}

type Entry struct {
	Name          string `json:"name"`
	NameEncoding  string `json:"name_encoding"`
	Value         string `json:"value"`
	ValueEncoding string `json:"value_encoding"`
}

type View struct {
	SaveState           string  `json:"save_state"`
	CapturedAt          *int64  `json:"captured_at"`
	HTTPStatus          *int    `json:"http_status"`
	BytesSaved          int64   `json:"bytes_saved"`
	CredentialsRedacted bool    `json:"credentials_redacted"`
	IPv4Redacted        bool    `json:"ipv4_redacted"`
	Headers             []Entry `json:"headers"`
}

func Capture(headers http.Header, host string, status int, now time.Time) Fact {
	f := Fact{state: "saved", capturedAt: now.Unix(), status: status}
	if f.capturedAt < 0 || f.capturedAt > 253402300799 {
		f.capturedAt = 0
	}
	var raw bytes.Buffer
	raw.WriteString("HF1")
	names := make([]string, 0, len(headers))
	size := 0
	for name, values := range headers {
		names = append(names, name)
		for _, value := range values {
			size += len(name) + len(value) + 4
			if size > MaxHeaderBytes {
				f.state = "too_large"
				return f
			}
		}
	}
	if host != "" {
		size += len("Host") + len(host) + 4
		if size > MaxHeaderBytes {
			f.state = "too_large"
			return f
		}
	}
	sort.Strings(names)
	appendPair := func(name, value string) {
		if CredentialName(name) {
			value = "[redacted]"
		}
		raw.Write(binary.AppendUvarint(nil, uint64(len(name))))
		raw.WriteString(name)
		raw.Write(binary.AppendUvarint(nil, uint64(len(value))))
		raw.WriteString(value)
	}
	for _, name := range names {
		for _, value := range headers[name] {
			appendPair(name, value)
		}
	}
	if host != "" {
		appendPair("Host", host)
	}
	if raw.Len() > MaxEncodedBytes {
		f.state = "too_large"
		return f
	}
	var compressed bytes.Buffer
	writer := compressionWriters.Get().(*gzip.Writer)
	writer.Reset(&compressed)
	_, err := writer.Write(raw.Bytes())
	closeErr := writer.Close()
	writer.Reset(io.Discard)
	compressionWriters.Put(writer)
	if err != nil || closeErr != nil {
		f.state = "unavailable"
		return f
	}
	f.data = compressed.Bytes()
	f.decodedBytes = raw.Len()
	return f
}

func text(value string) (string, string) {
	if utf8.ValidString(value) {
		return value, "utf-8"
	}
	return base64.StdEncoding.EncodeToString([]byte(value)), "base64"
}

// Decode must run after the authorized transaction releases its connection.
func Decode(state string, at *int64, status *int, compressed []byte, redactIPv4 bool) (View, error) {
	view, _, err := decode(state, at, status, compressed, redactIPv4, MaxEncodedBytes)
	return view, err
}
func decode(state string, at *int64, status *int, compressed []byte, redactIPv4 bool, limit int) (View, int, error) {
	view := View{SaveState: state, CapturedAt: at, HTTPStatus: status, BytesSaved: int64(len(compressed)), CredentialsRedacted: true, IPv4Redacted: redactIPv4, Headers: []Entry{}}
	if state != "saved" {
		return view, 0, nil
	}
	if len(compressed) > MaxEncodedBytes {
		return view, 0, errors.New("header facts exceed encoded limit")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return view, 0, errors.New("invalid header facts")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || len(raw) > limit || !bytes.HasPrefix(raw, []byte("HF1")) {
		return view, 0, errors.New("invalid header facts")
	}
	decodedBytes := len(raw)
	raw = raw[3:]
	take := func() (string, bool) {
		length, n := binary.Uvarint(raw)
		if n <= 0 || length > uint64(len(raw)-n) {
			return "", false
		}
		value := string(raw[n : n+int(length)])
		raw = raw[n+int(length):]
		return value, true
	}
	for len(raw) > 0 {
		name, ok := take()
		if !ok {
			return view, 0, errors.New("invalid header facts")
		}
		value, ok := take()
		if !ok {
			return view, 0, errors.New("invalid header facts")
		}
		if redactIPv4 {
			name = textprivacy.IPv4(name)
			value = textprivacy.IPv4(value)
		}
		entry := Entry{}
		entry.Name, entry.NameEncoding = text(name)
		entry.Value, entry.ValueEncoding = text(value)
		view.Headers = append(view.Headers, entry)
	}
	return view, decodedBytes, nil
}

var _ fmt.Stringer = Fact{}
