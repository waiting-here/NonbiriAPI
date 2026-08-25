package adminapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestValidateAdminJSONObjectStructuralBounds(t *testing.T) {
	if err := validateAdminJSONObject([]byte(`{"outer":{"items":[{"key":1},{"key":2}]}}`)); err != nil {
		t.Fatalf("valid object: %v", err)
	}
	for _, raw := range []string{
		`[]`,
		`{} {}`,
		`{"key":1,"key":2}`,
		`{"outer":{"key":1,"\u006bey":2}}`,
		`{"outer":` + strings.Repeat("[", maxAdminJSONDepth+1) + `0` + strings.Repeat("]", maxAdminJSONDepth+1) + `}`,
	} {
		if err := validateAdminJSONObject([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid structure: %.120q", raw)
		}
	}

	var many strings.Builder
	many.WriteByte('{')
	for i := 0; i <= maxAdminJSONFields; i++ {
		if i != 0 {
			many.WriteByte(',')
		}
		fmt.Fprintf(&many, "%q:0", fmt.Sprintf("f%d", i))
	}
	many.WriteByte('}')
	if err := validateAdminJSONObject([]byte(many.String())); err == nil {
		t.Fatal("accepted object above field budget")
	}
}

func TestReadAdminJSONBodyClearsMaxBytesPartialBuffer(t *testing.T) {
	limited := http.MaxBytesReader(
		httptest.NewRecorder(),
		io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{0x7f}, 32))),
		8,
	)
	defer limited.Close()
	raw, err := readAdminJSONBody(limited)
	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Fatalf("error=%v, want MaxBytesError", err)
	}
	if len(raw) == 0 {
		t.Fatal("MaxBytesReader did not expose a partial buffer to the clear path")
	}
	for index, value := range raw {
		if value != 0 {
			t.Fatalf("partial buffer byte %d=%d, want zero", index, value)
		}
	}
}

func TestValidateAdminJSONObjectExactRootFields(t *testing.T) {
	allowed := map[string]struct{}{
		"credits":      {},
		"operation_id": {},
	}
	for _, raw := range []string{
		`{"credits":"1"}`,
		`{"\u0063redits":"1","operation_id":"op"}`,
	} {
		if err := validateAdminJSONObjectExact([]byte(raw), allowed); err != nil {
			t.Fatalf("valid exact root key %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"CREDITS":"1"}`,
		`{"credits":"1","CREDITS":"2"}`,
		`{"credits":"1","\u0043REDITS":"2"}`,
		`{"Operation_Id":"op"}`,
	} {
		if err := validateAdminJSONObjectExact([]byte(raw), allowed); err == nil {
			t.Fatalf("accepted non-exact root key: %s", raw)
		}
	}
}

func TestExactAdminJSONRootFieldsRequiresExplicitTags(t *testing.T) {
	type taggedEmbedded struct {
		Inner int `json:"inner"`
	}
	type valid struct {
		Value    int            `json:"value,string"`
		Embedded taggedEmbedded `json:"embedded,omitempty"`
		Ignored  string         `json:"-"`
		hidden   string
	}
	fields, err := exactAdminJSONRootFields(&valid{})
	if err != nil {
		t.Fatalf("valid tagged destination: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("fields=%v", fields)
	}
	for _, key := range []string{"value", "embedded"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("missing exact tag %q in %v", key, fields)
		}
	}

	type untaggedField struct {
		Value int
	}
	type emptyTag struct {
		Value int `json:",omitempty"`
	}
	type TaggedEmbedded struct {
		Inner int `json:"inner"`
	}
	type untaggedAnonymous struct {
		TaggedEmbedded
	}
	duplicateTagType := reflect.StructOf([]reflect.StructField{
		{Name: "First", Type: reflect.TypeFor[int](), Tag: `json:"value"`},
		{Name: "Second", Type: reflect.TypeFor[int](), Tag: `json:"value,omitempty"`},
	})
	for name, destination := range map[string]any{
		"untagged exported field":  &untaggedField{},
		"empty tag name":           &emptyTag{},
		"duplicate explicit tag":   reflect.New(duplicateTagType).Interface(),
		"untagged anonymous field": &untaggedAnonymous{},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := exactAdminJSONRootFields(destination); err == nil {
				t.Fatal("accepted unsafe JSON destination")
			}
		})
	}

	type taggedAnonymous struct {
		TaggedEmbedded `json:"embedded,omitempty"`
	}
	fields, err = exactAdminJSONRootFields(&taggedAnonymous{})
	if err != nil {
		t.Fatalf("explicitly tagged anonymous field: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("anonymous fields=%v", fields)
	}
	if _, ok := fields["embedded"]; !ok {
		t.Fatalf("missing named anonymous field: %v", fields)
	}
}
