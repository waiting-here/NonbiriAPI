package headerfacts

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"
)

// Discovery currently permits at most twenty pages. Captures share an
// independent eight-MiB decoded and stored budget for the complete attempt.
const MaxResponses = 20
const MaxCollectionBytes = 8 << 20

type ResponseView struct {
	ResponseIndex int `json:"response_index"`
	View
}
type CollectionView struct {
	SaveState string         `json:"save_state"`
	Responses []ResponseView `json:"responses"`
}

func encodeCollection(facts []Fact, state string) Fact {
	fact := Fact{state: state, capturedAt: time.Now().Unix()}
	if state != "" {
		return fact
	}
	if len(facts) == 0 {
		return Fact{}
	}
	fact.state = "saved"
	fact.capturedAt = facts[0].capturedAt
	data := []byte("HC1")
	data = binary.AppendUvarint(data, uint64(len(facts)))
	for _, item := range facts {
		data = binary.AppendUvarint(data, uint64(item.capturedAt))
		data = binary.AppendUvarint(data, uint64(item.status))
		data = binary.AppendUvarint(data, uint64(len(item.data)))
		data = append(data, item.data...)
	}
	if len(data) > MaxCollectionBytes {
		fact.state = "too_large"
		return fact
	}
	fact.data = data
	return fact
}
func DecodeCollection(state string, data []byte, redactIPv4 bool) (CollectionView, error) {
	view := CollectionView{SaveState: state, Responses: []ResponseView{}}
	if state != "saved" {
		return view, nil
	}
	invalid := errors.New("invalid response header collection")
	if len(data) > MaxCollectionBytes || !bytes.HasPrefix(data, []byte("HC1")) {
		return view, invalid
	}
	data = data[3:]
	take := func() (uint64, bool) {
		value, n := binary.Uvarint(data)
		if n <= 0 {
			return 0, false
		}
		data = data[n:]
		return value, true
	}
	count, ok := take()
	if !ok || count < 1 || count > MaxResponses {
		return view, invalid
	}
	remaining := MaxCollectionBytes
	for i := 0; i < int(count); i++ {
		at, ok := take()
		if !ok || at > 253402300799 {
			return view, invalid
		}
		status, ok := take()
		if !ok || status < 100 || status > 999 {
			return view, invalid
		}
		size, ok := take()
		if !ok || size > uint64(len(data)) || size > MaxEncodedBytes {
			return view, invalid
		}
		timestamp, code := int64(at), int(status)
		item, decoded, err := decode("saved", &timestamp, &code, data[:int(size)], redactIPv4, min(remaining, MaxEncodedBytes))
		if err != nil {
			return view, invalid
		}
		remaining -= decoded
		view.Responses = append(view.Responses, ResponseView{ResponseIndex: i + 1, View: item})
		data = data[int(size):]
	}
	if len(data) != 0 {
		return view, invalid
	}
	return view, nil
}
