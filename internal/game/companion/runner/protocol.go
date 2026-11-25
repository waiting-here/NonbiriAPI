// Package runner exchanges bounded messages with the optional isolated worker.
package runner

import (
	"bytes"
	"encoding/json"
	"errors"
)

const SourceLimit = 256 << 10
const ModuleLimit = 8 << 20
const ReplyLimit = 64 << 10
const HistoryLimit = 128 << 20
const RequestLimit = 8 << 20
const DiagnosticLimit = 4096

type Job struct {
	WatchPeer bool            `json:"watch_peer,omitempty"`
	Module    []byte          `json:"module"`
	Request   json.RawMessage `json:"request"`
	Catalog   json.RawMessage `json:"catalog"`
}
type Message struct {
	Operation string          `json:"op,omitempty"`
	Args      json.RawMessage `json:"args,omitempty"`
	Reply     json.RawMessage `json:"reply,omitempty"`
}

type Buffer struct {
	Limit    int
	Bytes    bytes.Buffer
	Overflow bool
}

func (b *Buffer) Write(p []byte) (int, error) {
	room := b.Limit - b.Bytes.Len()
	if len(p) > room {
		b.Overflow = true
		_, _ = b.Bytes.Write(p[:room])
		return len(p), nil
	}
	return b.Bytes.Write(p)
}

var ErrWorker = errors.New("companion worker unavailable")
var ErrProgram = errors.New("companion program failed")
