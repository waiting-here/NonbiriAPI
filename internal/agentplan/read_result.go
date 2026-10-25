package agentplan

import (
	"encoding/json"
	"errors"
)

const ReadMaxItems = 5000
const ReadMaxBytes = 1 << 20

// ReadLimitError reports an incomplete read without exposing a matching count.
type ReadLimitError struct{}

func (*ReadLimitError) Error() string {
	return "matching read exceeds 5000 items or 1 MiB; narrow the selection"
}

func IsReadLimit(err error) bool {
	var limit *ReadLimitError
	return errors.As(err, &limit)
}

func CheckReadCount(count int) error {
	if count > ReadMaxItems {
		return &ReadLimitError{}
	}
	return nil
}

// ReadBudget bounds construction as well as the final serialized result.
type ReadBudget struct {
	items int
	bytes int
}

func (b *ReadBudget) Add(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return b.AddEncoded(raw)
}

// AddEncoded counts JSON bytes directly, without encoding a byte slice as base64.
func (b *ReadBudget) AddEncoded(raw json.RawMessage) error {
	if b.items >= ReadMaxItems || len(raw)+b.bytes > ReadMaxBytes {
		return &ReadLimitError{}
	}
	b.items++
	b.bytes += len(raw) + 1
	return nil
}

func MarshalRead(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err == nil && len(raw) > ReadMaxBytes {
		return nil, &ReadLimitError{}
	}
	return raw, err
}
