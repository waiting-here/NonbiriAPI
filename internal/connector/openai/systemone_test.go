package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const systemOneQuestions = `{"yes":{"type":"noul","instructions":{"nested":["question",true,1,null]},"criteria":{"true":"yes"}},"pick":{"type":"choice","criteria":{"a":null,"b":{"text":"other"}}},"rate":{"type":"score","criteria":["low",{"text":"high"}]},"locate":{"type":"box"}}`
const systemOneAnswers = `{"yes":{"type":"noul","noul":0.8},"pick":{"type":"choice","choice":"a","confidence":0.9,"probabilities":{"a":0.9,"b":0.1}},"rate":{"type":"score","score":0.6,"confidence":0.8,"legend":{"0":"low","1":{"text":"high"}},"probabilities":{"0":0.4,"1":0.6}},"locate":{"type":"box","box":{"top":0.1,"bottom":0.8,"left":0.2,"right":0.9},"confidence":null}}`

func TestSystemOneStrictRequestAndOptions(t *testing.T) {
	body := `{"model":"p/m","state":{"conversation":"opaque content","files":[1,2]},"questions":` + systemOneQuestions + `}`
	request := inferenceRequestForTest(t, contract.OperationSystemOne, body)
	if request.CheckTarget(contract.TypeTypeSafeSystemOne, contract.AttemptPolicy{}) == nil {
		t.Fatal("box without image opt-in accepted")
	}
	for _, values := range [][]string{{"false"}, {"True"}, {"true", "true"}, {"true, true"}} {
		request.SetOptionHeaders(http.Header{http.CanonicalHeaderKey(systemOneImagesHeader): values})
		if request.CheckTarget(contract.TypeTypeSafeSystemOne, contract.AttemptPolicy{}) == nil {
			t.Fatalf("accepted option %v", values)
		}
	}
	request.SetOptionHeaders(http.Header{http.CanonicalHeaderKey(systemOneImagesHeader): {"true"}})
	if request.CheckTarget(contract.TypeTypeSafeSystemOne, contract.AttemptPolicy{}) != nil || request.CheckTarget(contract.TypeOpenAICompatible, contract.AttemptPolicy{}) == nil {
		t.Fatal("System One protocol selection failed")
	}
	clone, err := request.WithLogicalBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Clear()
	if clone.CheckTarget(contract.TypeTypeSafeSystemOne, contract.AttemptPolicy{}) != nil {
		t.Fatal("adaptation lost explicit options")
	}
	for _, body := range []string{
		`{"model":"m","state":null,"questions":{"q":{"type":"noul"}}}`,
		`{"model":"m","state":{"n":1e1000},"questions":{"q":{"type":"noul"}}}`,
		`{"model":"m","state":"s","questions":{}}`,
		`{"model":"m","state":"s","questions":{"q":{"type":"noul","criteria":{"other":"x"}}}}`,
		`{"model":"m","state":"s","questions":{"q":{"type":"choice","criteria":{}}}}`,
		`{"model":"m","state":"s","questions":{"q":{"type":"score","criteria":[null]}}}`,
		`{"model":"m","state":"s","questions":{"q":{"type":"box","criteria":[]}}}`,
		`{"model":"m","state":"s","questions":{"q":{"type":"noul"}},"stream":true}`,
		`{"model":"m","state":"s","questions":{"q":{"type":"noul"}},"assets":{"a":{"part":"missing"}}}`,
	} {
		if r, err := DecodeInferenceRequest(strings.NewReader(body), contract.OperationSystemOne, 0); err == nil {
			r.Clear()
			t.Fatalf("accepted invalid request %s", body)
		}
	}
}

func TestSystemOneResponseProjectionAndUsage(t *testing.T) {
	request := inferenceRequestForTest(t, contract.OperationSystemOne, `{"model":"public/m","state":"state","questions":`+systemOneQuestions+`}`)
	raw := `{"model":"private","answers":` + systemOneAnswers + `,"usage":{"input_tokens":12,"output_tokens":4}}`
	body, usage, err := projectSystemOneResponse([]byte(raw), request)
	if err != nil || !usage.Present || usage.UncachedInputTokens != 12 || usage.OutputTokens != 4 || bytes.Contains(body, []byte("private")) {
		t.Fatalf("projection=%s usage=%+v err=%v", body, usage, err)
	}
	for _, changed := range []string{strings.Replace(raw, `"noul":0.8`, `"noul":1.1`, 1), strings.Replace(raw, `"choice":"a"`, `"choice":"missing"`, 1), strings.Replace(raw, `"0":"low"`, `"0":"other"`, 1), strings.Replace(raw, `"score":0.6`, `"score":2`, 1), strings.Replace(raw, `"top":0.1`, `"top":null`, 1)} {
		_, usage, err := projectSystemOneResponse([]byte(changed), request)
		if err == nil || !usage.Present || usage.OutputTokens != 4 {
			t.Fatal("invalid answer or valid usage lost")
		}
	}
	_, usage, err = projectSystemOneResponse([]byte(strings.Replace(raw, `"output_tokens":4`, `"output_tokens":null`, 1)), request)
	if err != nil || usage.Present {
		t.Fatal("unknown usage guessed")
	}
}

func systemOneMultipartForTest(t *testing.T, body string, names ...string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="request"`}, "Content-Type": {"application/json"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, body)
	for _, name := range names {
		part, err = writer.CreateFormFile(name, "image.png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte{0x89, 'P', 'N', 'G'})
	}
	_ = writer.Close()
	return buf.Bytes(), writer.FormDataContentType()
}
func TestSystemOneMultipartOwnershipAndDispatch(t *testing.T) {
	logical := `{"model":"public/m","state":"image-placeholder","questions":{"q":{"type":"noul"}},"assets":{"image-placeholder":{"type":"image","part":"image","data_base64":"ignored"}}}`
	for _, names := range [][]string{{}, {"image", "image"}, {"extra"}, {"image", "extra"}} {
		raw, media := systemOneMultipartForTest(t, logical, names...)
		m, err := DecodeInferenceMultipart(raw, media, contract.OperationSystemOne, 0)
		if err == nil {
			defer m.Clear()
			r, e := m.Decode(m.fields, contract.OperationSystemOne, 0)
			if e == nil {
				r.Clear()
				t.Fatalf("accepted parts %v", names)
			}
		}
	}
	raw, media := systemOneMultipartForTest(t, logical, "image")
	if m, err := DecodeInferenceMultipart(raw, media, contract.OperationSystemOne, int64(len(raw)-1)); err == nil {
		m.Clear()
		t.Fatal("body limit ignored")
	}
	m, err := DecodeInferenceMultipart(raw, media, contract.OperationSystemOne, 0)
	if err != nil {
		t.Fatal(err)
	}
	request, err := m.Decode(m.fields, contract.OperationSystemOne, 0)
	m.Clear()
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	request.SetOptionHeaders(http.Header{http.CanonicalHeaderKey(systemOneImagesHeader): {"true"}, http.CanonicalHeaderKey(systemOneLongHeader): {"true"}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Header.Get(systemOneImagesHeader) != "true" || r.Header.Get(systemOneLongHeader) != "true" || r.Header.Get("Authorization") != "Bearer secret-material" {
			t.Error("wrong request routing")
		}
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Error(err)
			return
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		part, err := reader.NextPart()
		if err != nil || part.FormName() != "request" || part.Header.Get("Content-Type") != "application/json" {
			t.Error("JSON part must be first")
			return
		}
		var object map[string]json.RawMessage
		if json.NewDecoder(part).Decode(&object) != nil || string(object["model"]) != `"private"` {
			t.Error("model not rewritten")
		}
		part, err = reader.NextPart()
		if err != nil || part.FormName() != "image" {
			t.Error("image missing")
			return
		}
		data, _ := io.ReadAll(part)
		if !bytes.Equal(data, []byte{0x89, 'P', 'N', 'G'}) {
			t.Error("upload bytes changed")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"private","answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":2,"output_tokens":1}}`)
	}))
	defer server.Close()
	writer := &decisionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
	result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeTypeSafeSystemOne, contract.AttemptPolicy{})
	if !result.Success || !result.Usage.Present || writer.started != 1 || writer.observed != 1 {
		t.Fatalf("result=%+v body=%s", result, writer.Body.String())
	}
}

func TestSystemOneOwnModelDiscovery(t *testing.T) {
	models, err := parseSystemOneModels([]byte(`{"models":[{"name":"jev-latest"},{"name":"jev-latest"},{"name":"jev-other"}]}`), nil)
	if err != nil || len(models) != 2 || models[0].ID != "jev-latest" {
		t.Fatalf("models=%v err=%v", models, err)
	}
	if _, err := parseSystemOneModels([]byte(`{"data":[{"id":"m"}]}`), nil); err == nil {
		t.Fatal("silently accepted another protocol")
	}
	guard := newSensitiveGuard([]byte("secret-material"))
	defer guard.Clear()
	if _, err := parseSystemOneModels([]byte(`{"models":[{"name":"secret-\u006daterial"}]}`), guard); err == nil {
		t.Fatal("decoded secret accepted")
	}
}

func TestSystemOneDiscoveryUsesSharedTransport(t *testing.T) {
	var sent *http.Request
	client := &discoveryEndpointClient{baseURL: "https://api.example/v1", do: func(r *http.Request) (*http.Response, error) {
		sent = r
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sk-discovery" {
			t.Fatal("wrong discovery dispatch")
		}
		return openAIDiscoveryResponse(200, `{"models":[{"name":"jev-latest","description":"model","release_date":"2026-09-15"}]}`), nil
	}}
	input := openAIDiscoveryInput(client, []byte("sk-discovery"), nil)
	input.Target = contract.NewTarget(contract.TypeTypeSafeSystemOne, "https://api.example/v1", "")
	result := (ModelDiscoverer{ConnectorType: contract.TypeTypeSafeSystemOne}).Discover(context.Background(), input)
	if !result.Succeeded() || len(result.Models) != 1 || result.Models[0].ID != "jev-latest" || sent.Header.Get("Authorization") != "" {
		t.Fatalf("discovery result=%+v", result)
	}
}
