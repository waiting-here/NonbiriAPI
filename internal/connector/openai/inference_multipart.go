package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
	"unicode/utf8"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
)

type inferencePart struct {
	name, filename, mediaType string
	data                      []byte
}

// InferenceMultipart separates uploaded bytes from the JSON policy envelope.
// Clear releases every upload; parsing never creates temporary files.
type InferenceMultipart struct {
	parts  []inferencePart
	fields []byte
}

func (m *InferenceMultipart) Clear() {
	if m != nil {
		for _, part := range m.parts {
			clear(part.data)
		}
		clear(m.fields)
		m.parts = nil
		m.fields = nil
	}
}
func (m *InferenceMultipart) LogicalBody() []byte {
	if m == nil {
		return nil
	}
	return bytes.Clone(m.fields)
}
func (m *InferenceMultipart) Decode(body []byte, operation contract.Operation, limit int64) (*InferenceRequest, error) {
	if m == nil {
		return nil, ErrInvalidRequest
	}
	return decodeInferenceRequest(bytes.NewReader(body), operation, limit, m.parts)
}

func SupportsInferenceMultipart(operation contract.Operation) bool {
	return operation == contract.OperationImagesEdits || operation == contract.OperationImagesVariations || operation == contract.OperationAudioTranscriptions || operation == contract.OperationAudioTranslations || operation == contract.OperationSystemOne
}

func DecodeInferenceMultipart(body []byte, mediaType string, operation contract.Operation, limit int64) (*InferenceMultipart, error) {
	if !SupportsInferenceMultipart(operation) {
		return nil, ErrInvalidRequest
	}
	if operation == contract.OperationSystemOne {
		return decodeSystemOneMultipart(body, mediaType, limit)
	}
	limit = requestbody.DecoderLimit(limit)
	if int64(len(body)) > limit {
		return nil, ErrPayloadTooLarge
	}
	media, params, err := mime.ParseMediaType(mediaType)
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return nil, invalidField("Content-Type", "expected multipart/form-data with a boundary")
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	result := &InferenceMultipart{}
	valid := false
	defer func() {
		if !valid {
			result.Clear()
		}
	}()
	fields := map[string]json.RawMessage{}
	arrays := map[string][]string{}
	partCount := 0
	for {
		part, err := r.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, invalidField("body", "invalid multipart body")
		}
		partCount++
		if partCount > 256 {
			_ = part.Close()
			return nil, ErrPayloadTooLarge
		}
		name := part.FormName()
		if name == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") || part.Header.Get("Content-Transfer-Encoding") != "" {
			_ = part.Close()
			return nil, invalidField("body", "invalid multipart field")
		}
		data, err := readBounded(part, limit)
		_ = part.Close()
		if err != nil {
			return nil, err
		}
		filename := part.FileName()
		if filename != "" {
			if len(data) == 0 || len(filename) > 256 || strings.ContainsAny(filename, "\x00\r\n") {
				clear(data)
				return nil, invalidField("file", "invalid upload")
			}
			media := part.Header.Get("Content-Type")
			if media == "" {
				media = "application/octet-stream"
			}
			if _, _, err := mime.ParseMediaType(media); err != nil {
				clear(data)
				return nil, invalidField("file", "invalid upload content type")
			}
			result.parts = append(result.parts, inferencePart{name: name, filename: filename, mediaType: media, data: data})
			continue
		}
		if !utf8.Valid(data) {
			clear(data)
			return nil, invalidField("body", "multipart text fields must be UTF-8")
		}
		if strings.HasSuffix(name, "[]") {
			base := strings.TrimSuffix(name, "[]")
			if _, ok := fields[base]; ok {
				clear(data)
				return nil, invalidField("body", "duplicate multipart field")
			}
			arrays[base] = append(arrays[base], string(data))
			clear(data)
			continue
		}
		if _, ok := fields[name]; ok || arrays[name] != nil {
			clear(data)
			return nil, invalidField("body", "duplicate multipart field")
		}
		var encoded []byte
		switch name {
		case "stream":
			if string(data) != "true" && string(data) != "false" {
				clear(data)
				return nil, invalidField(name, "expected a boolean")
			}
			encoded = bytes.Clone(data)
		case "n", "partial_images", "temperature", "speed":
			if !json.Valid(data) || bytes.ContainsAny(data, "\"[{ntf") {
				clear(data)
				return nil, invalidField(name, "expected a number")
			}
			encoded = bytes.Clone(data)
		case "chunking_strategy":
			if len(data) > 0 && data[0] == '{' && json.Valid(data) {
				encoded = bytes.Clone(data)
			} else {
				encoded, _ = json.Marshal(string(data))
			}
		default:
			encoded, _ = json.Marshal(string(data))
		}
		clear(data)
		fields[name] = encoded
	}
	defer func() {
		for _, raw := range fields {
			clear(raw)
		}
	}()
	for name, values := range arrays {
		fields[name], _ = json.Marshal(values)
	}
	result.fields, err = json.Marshal(fields)
	if err != nil || int64(len(result.fields)) > limit {
		return nil, ErrPayloadTooLarge
	}
	valid = true
	return result, nil
}

func cloneInferenceParts(parts []inferencePart) []inferencePart {
	copy := make([]inferencePart, len(parts))
	for i, part := range parts {
		copy[i] = part
		copy[i].data = bytes.Clone(part.data)
	}
	return copy
}
func (r *InferenceRequest) validateParts() error {
	if r.operation == contract.OperationSystemOne {
		return validateSystemOne(fieldsByName(r.fields), r.parts)
	}
	images, files, masks := 0, 0, 0
	for _, part := range r.parts {
		switch r.operation {
		case contract.OperationImagesEdits, contract.OperationImagesVariations:
			switch part.name {
			case "image", "image[]":
				images++
			case "mask":
				masks++
			default:
				return invalidField("file", "unsupported image upload field")
			}
		case contract.OperationAudioTranscriptions, contract.OperationAudioTranslations:
			if part.name != "file" {
				return invalidField("file", "unsupported audio upload field")
			}
			files++
		default:
			return ErrInvalidRequest
		}
	}
	if r.operation == contract.OperationImagesEdits && (images < 1 || images > 128 || masks > 1) || r.operation == contract.OperationImagesVariations && (images != 1 || masks != 0) || r.operation == contract.OperationAudioTranscriptions && files != 1 || r.operation == contract.OperationAudioTranslations && files != 1 {
		return invalidField("file", "invalid number of uploaded files")
	}
	return nil
}

func (r *InferenceRequest) marshalMultipart(object map[string]json.RawMessage) ([]byte, string, error) {
	if r.operation == contract.OperationSystemOne {
		return r.marshalSystemOneMultipart(object)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, raw := range object {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			var values []string
			if len(raw) > 0 && raw[0] == '[' && json.Unmarshal(raw, &values) == nil {
				for _, value := range values {
					if err := w.WriteField(name+"[]", value); err != nil {
						return nil, "", err
					}
				}
				continue
			}
			text = string(raw)
		}
		if err := w.WriteField(name, text); err != nil {
			return nil, "", err
		}
	}
	for _, part := range r.parts {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", "form-data; name="+strconv.Quote(part.name)+"; filename="+strconv.Quote(part.filename))
		header.Set("Content-Type", part.mediaType)
		dst, err := w.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err = dst.Write(part.data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	if int64(body.Len()) > r.RequestBodyLimit()+(64<<10) {
		clear(body.Bytes())
		return nil, "", ErrPayloadTooLarge
	}
	return body.Bytes(), w.FormDataContentType(), nil
}
