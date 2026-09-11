package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"
)

const DefaultMaxEmbeddingResponseBytes int64 = 32 << 20

// projectEmbeddingResponse borrows the raw response and emits only public
// protocol fields. Numeric vectors are checked one element at a time; unknown
// vendor fields never enter the caller response or its semantic leak scanner.
func projectEmbeddingResponse(body []byte, request *EmbeddingRequest) ([]byte, Usage, error) {
	invalid := func() ([]byte, Usage, error) { return nil, Usage{}, errInvalidUpstreamResponse }
	if request == nil || request.InputCount < 1 || request.InputCount > MaxEmbeddingBatch || int64(len(body)) > DefaultMaxEmbeddingResponseBytes || !validateEmbeddingJSON(body) {
		return invalid()
	}
	fields, ok := borrowedObjectFields(body)
	if !ok {
		return invalid()
	}
	root := fieldsByName(fields)
	if hasUpstreamError(root) || !exactString(root, "object", "list") || !requiredString(root, "model", MaxUpstreamModelRunes, true) {
		return invalid()
	}
	var out bytes.Buffer
	out.Grow(len(body) + 1024)
	complete := false
	defer func() {
		if !complete {
			clear(out.Bytes())
		}
	}()
	out.WriteString(`{"object":"list","data":[`)
	seen := make([]bool, request.InputCount)
	count, dimension := 0, 0
	valid := visitJSONArray(root["data"], func(raw []byte) bool {
		if count >= request.InputCount {
			return false
		}
		fields, ok := borrowedObjectFields(raw)
		if !ok {
			return false
		}
		item := fieldsByName(fields)
		index, ok := embeddingInteger(item["index"])
		if !ok || index >= int64(len(seen)) || seen[index] || !exactString(item, "object", "embedding") {
			return false
		}
		length, ok := embeddingVectorLength(item["embedding"], request.EncodingFormat)
		if !ok || length == 0 || (dimension != 0 && length != dimension) || (request.Dimensions != 0 && length != request.Dimensions) {
			return false
		}
		dimension = length
		seen[index] = true
		if count > 0 {
			out.WriteByte(',')
		}
		out.WriteString(`{"object":"embedding","index":`)
		out.Write(item["index"])
		out.WriteString(`,"embedding":`)
		out.Write(item["embedding"])
		out.WriteByte('}')
		count++
		return int64(out.Len()) <= DefaultMaxEmbeddingResponseBytes
	})
	if !valid || count != request.InputCount {
		return invalid()
	}
	out.WriteString(`],"model":`)
	model, err := json.Marshal(request.Model)
	if err != nil {
		return invalid()
	}
	out.Write(model)
	clear(model)
	usage := embeddingUsage(root["usage"])
	if usage.Present {
		value := strconv.FormatInt(usage.UncachedInputTokens, 10)
		out.WriteString(`,"usage":{"prompt_tokens":` + value + `,"total_tokens":` + value + `}`)
	}
	out.WriteByte('}')
	if int64(out.Len()) > DefaultMaxEmbeddingResponseBytes {
		return invalid()
	}
	complete = true
	return out.Bytes(), usage, nil
}

func embeddingVectorLength(raw []byte, encoding string) (int, bool) {
	if encoding == "float" {
		count := 0
		ok := visitJSONArray(raw, func(number []byte) bool {
			if len(number) == 0 || (number[0] != '-' && (number[0] < '0' || number[0] > '9')) {
				return false
			}
			value, err := strconv.ParseFloat(string(number), 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				return false
			}
			count++
			return true
		})
		return count, ok && count > 0
	}
	if encoding != "base64" || len(raw) < 2 || raw[0] != '"' {
		return 0, false
	}
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil || len(encoded) == 0 || len(encoded)%4 != 0 || strings.ContainsAny(encoded, "\r\n") {
		return 0, false
	}
	decoder := base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(encoded))
	buffer := make([]byte, 32<<10)
	defer clear(buffer)
	count := 0
	for {
		n, err := io.ReadFull(decoder, buffer)
		if (err != nil && err != io.EOF && err != io.ErrUnexpectedEOF) || n%4 != 0 {
			return 0, false
		}
		for offset := 0; offset < n; offset += 4 {
			bits := binary.LittleEndian.Uint32(buffer[offset : offset+4])
			if bits&0x7f800000 == 0x7f800000 {
				return 0, false
			}
		}
		count += n / 4
		if err != nil {
			return count, count > 0
		}
	}
}

func embeddingUsage(raw []byte) Usage {
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return Usage{}
	}
	root := fieldsByName(fields)
	prompt, promptOK := embeddingInteger(root["prompt_tokens"])
	total, totalOK := embeddingInteger(root["total_tokens"])
	if !promptOK || !totalOK || prompt != total {
		return Usage{}
	}
	return Usage{UncachedInputTokens: prompt, Present: true}
}
