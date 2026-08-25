package adminapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// The byte limit is the public admin-control contract. The structural limits
// keep validation work independently bounded even when an input is made from
// many one-byte names or deeply nested unknown values.
const (
	maxAdminJSONDepth  = 64
	maxAdminJSONFields = 1024
)

var errInvalidAdminJSON = errors.New("adminapi: invalid JSON object")

// decodeJSONBody accepts exactly one bounded, UTF-8 JSON object. It validates
// the token tree before decoding into dst so encoding/json cannot silently
// replace invalid UTF-8 or apply its last-value-wins behavior to duplicate
// decoded keys. Unknown target fields remain rejected by the second pass.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeAdminJSONBody(w, r, dst, false, maxAdminBodyBytes)
}

// decodeOptionalJSONBody differs only in permitting a wholly empty or
// whitespace-only body. Once a non-whitespace byte is present, the same strict
// single-object rules apply.
func decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeAdminJSONBody(w, r, dst, true, maxAdminBodyBytes)
}

// decodeLegalJSONBody retains the same strict structural checks as every
// other administrator mutation but permits the bounded JSON escaping
// overhead of one 65,536-byte legal document. The decoded value is still
// validated against the much smaller per-field limit by the site-config
// validator; this larger transport cap is not available to any other route.
func decodeLegalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeAdminJSONBody(w, r, dst, false, maxLegalAdminBodyBytes)
}

func decodeAdminJSONBody(w http.ResponseWriter, r *http.Request, dst any, optional bool, maxBodyBytes int64) bool {
	if r == nil || r.Body == nil {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := readAdminJSONBody(r.Body)
	if err != nil {
		writeDecodeErr(w, err)
		return false
	}
	defer clear(raw)
	if len(bytes.TrimSpace(raw)) == 0 {
		if optional {
			return true
		}
		writeDecodeErr(w, errInvalidAdminJSON)
		return false
	}
	rootFields, err := exactAdminJSONRootFields(dst)
	if err != nil {
		writeErr(w, httperr.New(httperr.CodeInternal, "request decoder unavailable"))
		return false
	}
	if err := validateAdminJSONObjectExact(raw, rootFields); err != nil {
		writeDecodeErr(w, err)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeDecodeErr(w, err)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeDecodeErr(w, err)
		return false
	}
	return true
}

// readAdminJSONBody clears any partial buffer before returning a read error.
// MaxBytesReader deliberately returns the bytes up to its bound together with
// MaxBytesError; those request bytes must not remain live while the error
// response is assembled. Successful buffers are cleared by the caller after
// both validation passes complete.
func readAdminJSONBody(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(reader)
	if err != nil {
		clear(raw)
	}
	return raw, err
}

// validateAdminJSONObject rejects duplicate decoded keys at every object
// depth. The token decoder resolves JSON escapes before keys enter each seen
// set, so (for example) "credits" and "\u0063redits" conflict.
func validateAdminJSONObject(raw []byte) error {
	return validateAdminJSONObjectExact(raw, nil)
}

func validateAdminJSONObjectExact(raw []byte, rootFields map[string]struct{}) error {
	if !utf8.Valid(raw) {
		return errInvalidAdminJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return errInvalidAdminJSON
	}
	delim, ok := first.(json.Delim)
	if !ok || delim != '{' {
		return errInvalidAdminJSON
	}
	fields := 0
	if err := scanAdminJSONObject(decoder, 1, &fields, rootFields); err != nil {
		return errInvalidAdminJSON
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errInvalidAdminJSON
	}
	return nil
}

func scanAdminJSONValue(decoder *json.Decoder, depth int, fields *int) error {
	if depth > maxAdminJSONDepth {
		return errInvalidAdminJSON
	}
	token, err := decoder.Token()
	if err != nil {
		return errInvalidAdminJSON
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		return scanAdminJSONObject(decoder, depth, fields, nil)
	case '[':
		for decoder.More() {
			if err := scanAdminJSONValue(decoder, depth+1, fields); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errInvalidAdminJSON
		}
		return nil
	default:
		return errInvalidAdminJSON
	}
}

func scanAdminJSONObject(decoder *json.Decoder, depth int, fields *int, allowed map[string]struct{}) error {
	if depth > maxAdminJSONDepth {
		return errInvalidAdminJSON
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return errInvalidAdminJSON
		}
		key, ok := keyToken.(string)
		if !ok {
			return errInvalidAdminJSON
		}
		if _, duplicate := seen[key]; duplicate {
			return errInvalidAdminJSON
		}
		if allowed != nil {
			if _, exact := allowed[key]; !exact {
				return errInvalidAdminJSON
			}
		}
		seen[key] = struct{}{}
		(*fields)++
		if *fields > maxAdminJSONFields {
			return errInvalidAdminJSON
		}
		if err := scanAdminJSONValue(decoder, depth+1, fields); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errInvalidAdminJSON
	}
	return nil
}

// exactAdminJSONRootFields derives the only accepted wire spellings from
// explicit json tags. encoding/json otherwise accepts case-insensitive field
// matches and exported untagged Go names; both are forbidden at this admin
// mutation boundary. Anonymous fields are safe only when explicitly tagged,
// in which case they are treated as one named (not flattened) JSON field.
// Tag options do not alter the name and remain enforced by the second decode.
func exactAdminJSONRootFields(dst any) (map[string]struct{}, error) {
	typeOf := reflect.TypeOf(dst)
	for typeOf != nil && typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	if typeOf == nil || typeOf.Kind() != reflect.Struct {
		return nil, errors.New("adminapi: JSON destination must be a tagged struct pointer")
	}
	allowed := make(map[string]struct{}, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		if field.PkgPath != "" { // unexported fields are invisible to encoding/json
			continue
		}
		tag, tagged := field.Tag.Lookup("json")
		if !tagged {
			return nil, errors.New("adminapi: exported JSON field lacks an explicit tag")
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if !validAdminJSONTagName(name) {
			return nil, errors.New("adminapi: JSON field tag is invalid")
		}
		if _, duplicate := allowed[name]; duplicate {
			return nil, errors.New("adminapi: duplicate JSON field tag")
		}
		allowed[name] = struct{}{}
	}
	return allowed, nil
}

func validAdminJSONTagName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

func writeDecodeErr(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeErr(w, httperr.New(httperr.CodePayloadTooLarge, "request body too large"))
		return
	}
	writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
}
