package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	MaxTranslatedRequestBytes  int64 = 8 << 20
	MaxCallerJSONResponseBytes int64 = 8 << 20
	MaxCallerStreamBytes       int64 = 32 << 20
	MaxCallerSSEEventBytes           = 1 << 20
	maxSafeStringChunkBytes          = 128 << 10
)

var errJSONOutputLimit = errors.New("anthropic connector: translated JSON exceeded its limit")

type cappedJSONBuffer struct {
	data  []byte
	limit int64
}

func (b *cappedJSONBuffer) Write(value []byte) (int, error) {
	if b == nil || b.limit < 0 || int64(len(value)) > b.limit-int64(len(b.data)) {
		return 0, errJSONOutputLimit
	}
	b.data = append(b.data, value...)
	return len(value), nil
}

func (b *cappedJSONBuffer) clear() {
	if b == nil {
		return
	}
	clear(b.data)
	b.data = nil
}

// marshalJSONNoEscapeLimited uses Encoder rather than Marshal so translated
// prompts and model output do not acquire Go's optional HTML escaping. The
// encoder's trailing newline is not part of either public wire.
func marshalJSONNoEscapeLimited(value any, limit int64) ([]byte, error) {
	if limit < 1 {
		return nil, errJSONOutputLimit
	}
	buffer := &cappedJSONBuffer{limit: limit + 1}
	defer buffer.clear()
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	if len(buffer.data) == 0 || buffer.data[len(buffer.data)-1] != '\n' {
		return nil, io.ErrUnexpectedEOF
	}
	body := buffer.data[:len(buffer.data)-1]
	if int64(len(body)) > limit {
		return nil, errJSONOutputLimit
	}
	return bytes.Clone(body), nil
}
