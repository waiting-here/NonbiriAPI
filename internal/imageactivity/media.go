package imageactivity

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"strconv"
)

func readBounded(reader io.Reader, limit int) ([]byte, error) {
	chunks := [][]byte{}
	total := 0
	var terminal error
	for total <= limit {
		size := 32768
		if size > limit+1-total {
			size = limit + 1 - total
		}
		part := make([]byte, size)
		filled, empty := 0, 0
		for filled < len(part) {
			n, err := reader.Read(part[filled:])
			if n < 0 || n > len(part)-filled {
				return nil, ErrInvalid
			}
			filled += n
			if err != nil {
				terminal = err
				break
			}
			if n == 0 {
				empty++
				if empty >= 100 {
					terminal = io.ErrNoProgress
					break
				}
			} else {
				empty = 0
			}
		}
		if filled > 0 {
			chunks = append(chunks, part[:filled])
			total += filled
		}
		if terminal != nil {
			break
		}
	}
	if terminal == io.EOF {
		terminal = nil
	}
	if total > limit {
		terminal = ErrCapacity
		total = limit
	}
	out := make([]byte, total)
	offset := 0
	for _, part := range chunks {
		offset += copy(out[offset:], part)
	}
	return out, terminal
}

// asciiJSONString decodes one JSON string directly into the base64 reader.
// It avoids retaining an additional full-size decoded base64 string.
type asciiJSONString struct {
	raw []byte
	pos int
}

func (r *asciiJSONString) Read(dst []byte) (int, error) {
	if r.pos >= len(r.raw) {
		return 0, io.EOF
	}
	n := 0
	for n < len(dst) && r.pos < len(r.raw) {
		b := r.raw[r.pos]
		r.pos++
		if b == '\\' {
			if r.pos >= len(r.raw) {
				return n, ErrInvalid
			}
			b = r.raw[r.pos]
			r.pos++
			switch b {
			case '\\', '/', '"':
			case 'n':
				b = '\n'
			case 'r':
				b = '\r'
			case 't':
				b = '\t'
			case 'u':
				if r.pos+4 > len(r.raw) {
					return n, ErrInvalid
				}
				v, err := strconv.ParseUint(string(r.raw[r.pos:r.pos+4]), 16, 16)
				if err != nil || v > 127 {
					return n, ErrInvalid
				}
				r.pos += 4
				b = byte(v)
			default:
				return n, ErrInvalid
			}
		}
		if b > 127 {
			return n, ErrInvalid
		}
		dst[n] = b
		n++
	}
	return n, nil
}
func decodeImageBase64(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, ErrInvalid
	}
	reader := base64.NewDecoder(base64.StdEncoding, &asciiJSONString{raw: raw[1 : len(raw)-1]})
	body, err := readBounded(reader, maxImage)
	if err != nil {
		return nil, err
	}
	compact := make([]byte, len(body))
	copy(compact, body)
	return compact, nil
}
func checkedImage(body []byte, declared string) (imageBytes, error) {
	if len(body) == 0 || len(body) > maxImage {
		return imageBytes{}, ErrInvalid
	}
	sniff := http.DetectContentType(body)
	if sniff != "image/png" && sniff != "image/jpeg" && sniff != "image/webp" {
		return imageBytes{}, ErrInvalid
	}
	if declared != "" {
		declared, _, err := mime.ParseMediaType(declared)
		if err != nil {
			return imageBytes{}, ErrInvalid
		}
		if declared != "application/octet-stream" && declared != sniff {
			return imageBytes{}, ErrInvalid
		}
	}
	var width, height int
	if sniff == "image/webp" {
		var err error
		width, height, err = webpDimensions(body)
		if err != nil {
			return imageBytes{}, err
		}
	} else {
		c, format, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil {
			return imageBytes{}, ErrInvalid
		}
		if sniff == "image/png" && format != "png" || sniff == "image/jpeg" && format != "jpeg" {
			return imageBytes{}, ErrInvalid
		}
		width, height = c.Width, c.Height
		if sniff == "image/png" && (len(body) < 12 || !bytes.Equal(body[len(body)-8:len(body)-4], []byte("IEND"))) {
			return imageBytes{}, ErrInvalid
		}
		if sniff == "image/jpeg" && (len(body) < 2 || body[len(body)-2] != 255 || body[len(body)-1] != 217) {
			return imageBytes{}, ErrInvalid
		}
	}
	if width <= 0 || height <= 0 || int64(width) > 64000000/int64(height) {
		return imageBytes{}, ErrInvalid
	}
	return imageBytes{data: body, mime: sniff}, nil
}
func webpDimensions(body []byte) (int, int, error) {
	if len(body) < 20 || !bytes.Equal(body[:4], []byte("RIFF")) || !bytes.Equal(body[8:12], []byte("WEBP")) || uint64(binary.LittleEndian.Uint32(body[4:8]))+8 != uint64(len(body)) {
		return 0, 0, ErrInvalid
	}
	width, height := 0, 0
	frame := false
	le24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
	for pos := 12; pos+8 <= len(body); {
		size := uint64(binary.LittleEndian.Uint32(body[pos+4 : pos+8]))
		end := uint64(pos) + 8 + size
		if end > uint64(len(body)) {
			return 0, 0, ErrInvalid
		}
		chunk := body[pos+8 : int(end)]
		switch string(body[pos : pos+4]) {
		case "VP8X":
			if len(chunk) != 10 {
				return 0, 0, ErrInvalid
			}
			width = le24(chunk[4:7]) + 1
			height = le24(chunk[7:10]) + 1
		case "VP8 ":
			if len(chunk) < 10 || !bytes.Equal(chunk[3:6], []byte{157, 1, 42}) {
				return 0, 0, ErrInvalid
			}
			w, h := int(binary.LittleEndian.Uint16(chunk[6:8])&16383), int(binary.LittleEndian.Uint16(chunk[8:10])&16383)
			if width == 0 {
				width, height = w, h
			}
			frame = true
		case "VP8L":
			if len(chunk) < 5 || chunk[0] != 47 {
				return 0, 0, ErrInvalid
			}
			bits := binary.LittleEndian.Uint32(chunk[1:5])
			w, h := int(bits&16383)+1, int((bits>>14)&16383)+1
			if width == 0 {
				width, height = w, h
			}
			frame = true
		case "ANMF":
			if len(chunk) < 24 || width == 0 {
				return 0, 0, ErrInvalid
			}
			frame = true
		}
		pos = int(end) + int(size&1)
		if pos > len(body) {
			return 0, 0, ErrInvalid
		}
	}
	if !frame || width <= 0 || height <= 0 {
		return 0, 0, ErrInvalid
	}
	return width, height, nil
}
func responseReadError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrCapacity) {
		return "response_too_large"
	}
	return "invalid_result"
}
