package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type accumulationSink struct {
	header http.Header
	bytes  int
}

func (w *accumulationSink) Header() http.Header         { return w.header }
func (w *accumulationSink) WriteHeader(int)             {}
func (w *accumulationSink) Flush()                      {}
func (w *accumulationSink) Write(p []byte) (int, error) { w.bytes += len(p); return len(p), nil }

func accumulationFrames(total, chunk int) [][]byte {
	frames := [][]byte{}
	delta := strings.Repeat("x", chunk)
	for remaining := total; remaining > 0; remaining -= min(chunk, remaining) {
		value := delta[:min(chunk, remaining)]
		field := fmt.Sprintf("\"content\":%q", value)
		frames = append(frames, []byte(fmt.Sprintf("data: {\"id\":\"c\",\"created\":1,\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{%s},\"finish_reason\":null}]}\n\n", field)))
	}
	reason := "stop"
	frames = append(frames, []byte(fmt.Sprintf("data: {\"id\":\"c\",\"created\":1,\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\n", reason)), []byte("data: [DONE]\n\n"))
	return frames
}

// Exercise the real consumers and terminal projection, without network latency.
func BenchmarkStreamAccumulation(b *testing.B) {
	for _, total := range []int{64 << 10, 512 << 10} {
		for _, chunk := range []int{32, 512} {
			frames := accumulationFrames(total, chunk)
			for _, kind := range []string{"responses", "flatten"} {
				b.Run(fmt.Sprintf("%s/bytes=%d/chunk=%d", kind, total, chunk), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(total))
					for b.Loop() {
						sink := &accumulationSink{header: make(http.Header)}
						if kind == "responses" {
							request := &InferenceRequest{Model: "public", Stream: true, operation: contract.OperationResponses}
							projection, err := NewGatewayResponsesChatProjection(context.Background(), sink, request, DefaultMaxStreamBytes)
							if err != nil {
								b.Fatal(err)
							}
							for _, frame := range frames {
								if _, err = projection.Write(frame); err != nil {
									b.Fatal(err)
								}
							}
							if !projection.Complete() || sink.bytes == 0 {
								b.Fatal("incomplete response")
							}
							projection.Clear()
						} else {
							projection := NewFlattenSink(sink, DefaultMaxStreamBytes, int(DefaultMaxStreamBytes), nil)
							for _, frame := range frames {
								if _, err := projection.Write(frame); err != nil {
									b.Fatal(err)
								}
							}
							if err := projection.Complete(time.Second); err != nil {
								b.Fatal(err)
							}
							projection.Clear()
						}
					}
				})
			}
		}
	}
}
