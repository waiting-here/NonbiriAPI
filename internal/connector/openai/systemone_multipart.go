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

	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
)

func decodeSystemOneMultipart(body []byte, mediaType string, limit int64) (*InferenceMultipart, error) {
	limit = requestbody.DecoderLimit(limit)
	if int64(len(body)) > limit {
		return nil, ErrPayloadTooLarge
	}
	media, params, err := mime.ParseMediaType(mediaType)
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return nil, ErrInvalidRequest
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	result := &InferenceMultipart{}
	valid := false
	defer func() {
		if !valid {
			result.Clear()
		}
	}()
	seen := map[string]bool{}
	for count := 0; ; count++ {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 256 {
			return nil, ErrInvalidRequest
		}
		name := part.FormName()
		if name == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") || seen[name] || part.Header.Get("Content-Transfer-Encoding") != "" {
			_ = part.Close()
			return nil, ErrInvalidRequest
		}
		seen[name] = true
		data, err := readBounded(part, limit)
		_ = part.Close()
		if err != nil {
			return nil, err
		}
		if count == 0 {
			media, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if name != "request" || err != nil || media != "application/json" || !validateEmbeddingJSON(data) {
				clear(data)
				return nil, invalidField("request", "the first multipart part must contain the JSON request")
			}
			result.fields = data
		} else {
			filename := part.FileName()
			if len(data) == 0 || len(filename) > 256 || strings.ContainsAny(filename, "\x00\r\n") {
				clear(data)
				return nil, ErrInvalidRequest
			}
			result.parts = append(result.parts, inferencePart{name: name, filename: filename, mediaType: "application/octet-stream", data: data})
		}
	}
	if result.fields == nil {
		return nil, ErrInvalidRequest
	}
	// Shape and cross-references are checked with the policy-filtered request.
	valid = true
	return result, nil
}

func (r *InferenceRequest) marshalSystemOneMultipart(object map[string]json.RawMessage) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	raw, err := json.Marshal(object)
	if err != nil {
		return nil, "", err
	}
	defer clear(raw)
	header := textproto.MIMEHeader{"Content-Disposition": {`form-data; name="request"`}, "Content-Type": {"application/json"}}
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err = part.Write(raw); err != nil {
		return nil, "", err
	}
	for _, upload := range r.parts {
		header = textproto.MIMEHeader{"Content-Disposition": {"form-data; name=" + strconv.Quote(upload.name) + "; filename=" + strconv.Quote(upload.filename)}, "Content-Type": {upload.mediaType}}
		part, err = writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err = part.Write(upload.data); err != nil {
			return nil, "", err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, "", err
	}
	if int64(body.Len()) > r.RequestBodyLimit()+(64<<10) {
		clear(body.Bytes())
		return nil, "", ErrPayloadTooLarge
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}
