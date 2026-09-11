package openai

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func embeddingResponse(encoding, usage string) string {
	vectors := []string{`[0.25,-1.5]`, `[0,2e-3]`}
	if encoding == "base64" {
		vectors = []string{`"AACAPwAAAEA="`, `"AAAAAAAAgD8="`}
	}
	return `{"object":"list","data":[{"object":"embedding","index":1,"embedding":` + vectors[1] + `,"private":{"endpoint":"hidden"}},{"object":"embedding","index":0,"embedding":` + vectors[0] + `}],"model":"private/upstream","usage":` + usage + `,"vendor":{"secret":"not-public"}}`
}

func TestEmbeddingProjectionPreservesVectorsAndSeparatesUsage(t *testing.T) {
	for _, encoding := range []string{"float", "base64"} {
		for _, tc := range []struct {
			usage   string
			present bool
			tokens  int64
		}{
			{`{"prompt_tokens":0,"total_tokens":0}`, true, 0},
			{`{"prompt_tokens":3,"total_tokens":3,"prompt_tokens_details":{"cached_tokens":2}}`, true, 3},
			{`null`, false, 0}, {`[]`, false, 0}, {`{"prompt_tokens":3}`, false, 0},
			{`{"prompt_tokens":3,"total_tokens":4}`, false, 0}, {`{"prompt_tokens":-1,"total_tokens":-1}`, false, 0},
			{`{"prompt_tokens":1.0,"total_tokens":1}`, false, 0}, {`{"prompt_tokens":2147483648,"total_tokens":2147483648}`, false, 0},
		} {
			t.Run(encoding+"/"+tc.usage, func(t *testing.T) {
				r := &EmbeddingRequest{Model: "public/model", InputCount: 2, EncodingFormat: encoding, Dimensions: 2}
				out, usage, err := projectEmbeddingResponse([]byte(embeddingResponse(encoding, tc.usage)), r)
				if err != nil {
					t.Fatal(err)
				}
				defer clear(out)
				if usage.Present != tc.present || usage.UncachedInputTokens != tc.tokens || usage.CacheReadInputTokens != 0 || usage.OutputTokens != 0 {
					t.Fatalf("usage=%+v", usage)
				}
				var root struct {
					Model string
					Data  []struct {
						Index     int
						Embedding json.RawMessage
					}
					Usage json.RawMessage
				}
				if err := json.Unmarshal(out, &root); err != nil {
					t.Fatal(err)
				}
				if root.Model != "public/model" || len(root.Data) != 2 || root.Data[0].Index != 1 || root.Data[1].Index != 0 {
					t.Fatalf("projection=%s", out)
				}
				if (len(root.Usage) > 0) != tc.present {
					t.Fatalf("usage presence=%s", out)
				}
				if strings.Contains(string(out), "private") || strings.Contains(string(out), "vendor") || strings.Contains(string(out), "cached_tokens") {
					t.Fatal("unknown upstream fields escaped")
				}
			})
		}
	}
}

func TestEmbeddingProjectionRejectsInvalidVectorEnvelopes(t *testing.T) {
	base := embeddingResponse("float", `{"prompt_tokens":3,"total_tokens":3}`)
	for _, tc := range []struct{ from, to string }{
		{`"object":"list"`, `"object":"other"`}, {`"object":"embedding"`, `"object":"wrong"`},
		{`"index":1`, `"index":0`}, {`"index":1`, `"index":2`}, {`"index":1`, `"index":1e0`},
		{`[0.25,-1.5]`, `[]`}, {`[0.25,-1.5]`, `[1]`}, {`[0.25,-1.5]`, `[null,1]`}, {`[0.25,-1.5]`, `["1",2]`},
		{`[0.25,-1.5]`, `[1e400,1]`}, {`"model":"private/upstream"`, `"model":""`},
		{`"object":"list"`, `"object":"list","object":"list"`},
		{`"private":{"endpoint":"hidden"}`, `"private":{"x":1,"x":2}`},
		{`"object":"list"`, `"object":"list","error":{"message":"bad"}`},
	} {
		body := strings.Replace(base, tc.from, tc.to, 1)
		if out, _, err := projectEmbeddingResponse([]byte(body), &EmbeddingRequest{Model: "p/m", InputCount: 2, EncodingFormat: "float"}); err == nil {
			clear(out)
			t.Fatalf("accepted %s", tc.to)
		}
	}
	for _, count := range []int{0, 1, 3, 2049} {
		if _, _, err := projectEmbeddingResponse([]byte(base), &EmbeddingRequest{Model: "p/m", InputCount: count, EncodingFormat: "float"}); err == nil {
			t.Fatal("bad input count accepted")
		}
	}
	if _, _, err := projectEmbeddingResponse([]byte(base), &EmbeddingRequest{Model: "p/m", InputCount: 2, Dimensions: 3, EncodingFormat: "float"}); err == nil {
		t.Fatal("wrong dimensions accepted")
	}
	if _, _, err := projectEmbeddingResponse([]byte(base+"{}"), &EmbeddingRequest{Model: "p/m", InputCount: 2, EncodingFormat: "float"}); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestEmbeddingBase64RequiresCanonicalFiniteFloat32(t *testing.T) {
	for _, raw := range []string{`""`, `"AQID"`, `"AACAPw"`, `"AACAPx=="`, `"AAAA_A=="`, `"AACAPw==\n"`, `null`, `[1]`} {
		if _, ok := embeddingVectorLength([]byte(raw), "base64"); ok {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, bits := range []uint32{math.Float32bits(float32(math.Inf(1))), math.Float32bits(float32(math.Inf(-1))), 0x7fc00000} {
		var raw [4]byte
		binary.LittleEndian.PutUint32(raw[:], bits)
		if _, ok := embeddingVectorLength([]byte(fmt.Sprintf("%q", base64.StdEncoding.EncodeToString(raw[:]))), "base64"); ok {
			t.Fatal("nonfinite float accepted")
		}
	}
	if n, ok := embeddingVectorLength([]byte(`"AACAPw=="`), "base64"); !ok || n != 1 {
		t.Fatal("valid float rejected")
	}
}

func FuzzEmbeddingJSONValidation(f *testing.F) {
	f.Add([]byte(`{"model":"p/m","input":[[0],[1]],"extra":{"x":[]}}`))
	f.Add([]byte(embeddingResponse("float", `null`)))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		if validateEmbeddingJSON(data) {
			fields, ok := borrowedObjectFields(data)
			if ok {
				for _, field := range fields {
					if !json.Valid(field.value) {
						t.Fatal("invalid borrowed span")
					}
				}
			}
		}
	})
}
