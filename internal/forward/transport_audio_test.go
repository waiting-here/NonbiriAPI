package forward

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestTransportAudioMergesDecodedBytesAndTranscript(t *testing.T) {
	var stream bytes.Buffer
	for _, audio := range []map[string]any{
		{"id": "audio_1", "data": "YQ==", "transcript": "first "},
		{"data": "YmM=", "transcript": "second", "expires_at": 123456},
	} {
		frame, _ := json.Marshal(map[string]any{"id": "c1", "object": "chat.completion.chunk", "created": 1, "model": "m", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "audio": audio}, "finish_reason": nil}}})
		stream.WriteString("data: ")
		stream.Write(frame)
		stream.WriteString("\n\n")
	}
	stream.WriteString("data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	body, err := streamToCompletion(stream.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	root, err := readWireObject(body)
	if err != nil {
		t.Fatal(err)
	}
	audio := root["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["audio"].(map[string]any)
	if audio["data"] != "YWJj" || audio["transcript"] != "first second" || audio["id"] != "audio_1" || audio["expires_at"] != json.Number("123456") {
		t.Fatalf("audio changed: %+v", audio)
	}
	if err := mergeAudio(map[string]any{"id": "first"}, map[string]any{"id": "second"}); err == nil {
		t.Fatal("conflicting audio IDs accepted")
	}
	if err := mergeAudio(map[string]any{}, map[string]any{"data": "not-base64"}); err == nil {
		t.Fatal("invalid audio accepted")
	}
}

func TestTransportLargeAudioRoundTrip(t *testing.T) {
	root, err := readWireObject(transportLimitCompletion(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	message := root["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	message["audio"] = map[string]any{"id": "audio_1", "data": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0, 128, 255}, 100000)), "transcript": strings.Repeat("你好🙂", 50000), "expires_at": json.Number("123456")}
	raw, _ := json.Marshal(root)
	frames, err := completionToStream(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	assertTransportFrameBudgets(t, frames)
	body, err := streamToCompletion(bytes.Join(frames, nil))
	if err != nil {
		t.Fatal(err)
	}
	if canonicalTransportJSON(t, raw) != canonicalTransportJSON(t, body) {
		t.Fatal("audio/transcript/metadata changed during JSON/SSE roundtrip")
	}
}

func TestTransportContinuationPreservesIncrementalReasoningAndNativeSnapshot(t *testing.T) {
	root := map[string]any{}
	for _, id := range []string{"rs_1", "rs_2"} {
		if err := mergeDelta(root, map[string]any{"extra_content": map[string]any{"openai": map[string]any{"reasoning": []any{map[string]any{"id": id, "type": "reasoning", "encrypted_content": "opaque"}}}, "nonbiri": map[string]any{"continuation_id": "token"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := mergeDelta(root, map[string]any{"extra_content": map[string]any{"anthropic": map[string]any{"content": []any{map[string]any{"type": "thinking", "thinking": "thought", "signature": "signed"}}}}}); err != nil {
		t.Fatal(err)
	}
	value := materializeWire(root).(map[string]any)["extra_content"].(map[string]any)
	if len(value["openai"].(map[string]any)["reasoning"].([]any)) != 2 || value["nonbiri"].(map[string]any)["continuation_id"] != "token" {
		t.Fatalf("continuation altered: %+v", value)
	}
	if value["anthropic"].(map[string]any)["content"].([]any)[0].(map[string]any)["signature"] != "signed" {
		t.Fatal("signature altered")
	}
}

func TestTransportLargeContinuationNeverDropsSnapshotParts(t *testing.T) {
	for _, provider := range []string{"anthropic", "google", "gateway", "openai"} {
		t.Run(provider, func(t *testing.T) {
			root, err := readWireObject(transportLimitCompletion(t, ""))
			if err != nil {
				t.Fatal(err)
			}
			message := root["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
			key := "content"
			if provider == "google" {
				key = "parts"
			} else if provider == "openai" {
				key = "reasoning"
			}
			message["extra_content"] = map[string]any{provider: map[string]any{key: []any{
				map[string]any{"text": strings.Repeat("a", 150<<10), "signature": "first"},
				map[string]any{"text": strings.Repeat("b", 150<<10), "signature": "second"},
			}}, "nonbiri": map[string]any{"continuation_id": strings.Repeat("a", 43)}}
			raw, _ := json.Marshal(root)
			frames, err := completionToStream(raw, true)
			if provider != "openai" {
				if err == nil || len(frames) != 0 {
					t.Fatal("oversized immutable history was fragmented")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertTransportFrameBudgets(t, frames)
			body, err := streamToCompletion(bytes.Join(frames, nil))
			if err != nil || canonicalTransportJSON(t, raw) != canonicalTransportJSON(t, body) {
				t.Fatalf("incremental reasoning changed: %v", err)
			}
		})
	}
}
