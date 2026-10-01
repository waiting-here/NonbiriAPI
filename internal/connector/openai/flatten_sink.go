package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/tooltext"
)

// FlattenSink translates validated normalized tool deltas. It emits ordinary
// text immediately and holds tool material until the protocol has succeeded.
type FlattenSink struct {
	writer        http.ResponseWriter
	states        map[int]*tooltext.StreamChoiceState
	first         map[string]json.RawMessage
	usage         []byte
	tools         bool
	done          bool
	written       int64
	limit         int64
	maxFrame      int
	reject        func([]byte, []byte) bool
	projectionErr error
}

func NewFlattenSink(writer http.ResponseWriter, limit int64, maxFrame int, reject func([]byte, []byte) bool) *FlattenSink {
	return &FlattenSink{writer: writer, states: make(map[int]*tooltext.StreamChoiceState), limit: limit, maxFrame: maxFrame, reject: reject}
}
func (s *FlattenSink) Header() http.Header         { return s.writer.Header() }
func (s *FlattenSink) WriteHeader(status int)      { s.writer.WriteHeader(status) }
func (s *FlattenSink) Unwrap() http.ResponseWriter { return s.writer }
func (s *FlattenSink) Flush() {
	if s.written == 0 {
		return
	}
	if f, ok := s.writer.(http.Flusher); ok {
		f.Flush()
	}
}
func (s *FlattenSink) Write(frame []byte) (n int, err error) {
	defer func() {
		if errors.Is(err, tooltext.ErrToolFlatten) || errors.Is(err, tooltext.ErrFlattenStreamRejected) {
			s.projectionErr = err
		}
	}()
	data := bytes.TrimSpace(bytes.TrimPrefix(frame, []byte("data:")))
	if bytes.Equal(data, []byte("[DONE]")) {
		s.done = true
		return len(frame), nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return 0, tooltext.ErrToolFlatten
	}
	if _, ok := root["error"]; ok {
		return s.write(frame)
	}
	root, tools, err := tooltext.AccumulateStreamChunk(data, s.states)
	if err != nil {
		return 0, err
	}
	if s.first == nil {
		s.first = root
	}
	s.tools = s.tools || tools
	if s.tools {
		if usage, ok := root["usage"]; ok && !bytes.Equal(bytes.TrimSpace(usage), []byte("null")) {
			clear(s.usage)
			s.usage = append([]byte(nil), frame...)
		}
		return len(frame), nil
	}
	n, err = s.write(frame)
	if err == nil {
		tooltext.MarkStreamContentEmitted(s.states)
	}
	return n, err
}
func (s *FlattenSink) write(frame []byte) (int, error) {
	if len(frame) > s.maxFrame || int64(len(frame)) > s.limit-s.written {
		s.projectionErr = tooltext.ErrToolFlatten
		return 0, s.projectionErr
	}
	payload := streamFramePayload(frame)
	if !bytes.Equal(payload, []byte("[DONE]")) && s.reject != nil && s.reject(frame, payload) {
		s.projectionErr = tooltext.ErrFlattenStreamRejected
		return 0, s.projectionErr
	}
	n, err := s.writer.Write(frame)
	s.written += int64(n)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	return n, err
}
func (s *FlattenSink) Committed() bool        { return s.written > 0 }
func (s *FlattenSink) ProjectionError() error { return s.projectionErr }
func (s *FlattenSink) Clear() {
	tooltext.ClearStreamStates(s.states)
	s.states = nil
	clear(s.usage)
	s.usage = nil
	for _, raw := range s.first {
		clear(raw)
	}
	s.first = nil
}
func (s *FlattenSink) Complete(writeTimeout time.Duration) error {
	controller := http.NewResponseController(s.writer)
	if err := controller.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	if !s.done {
		return tooltext.ErrToolFlatten
	}
	if s.tools {
		completion, err := tooltext.StreamCompletionBodyAfter(s.first, s.states)
		if err != nil {
			return err
		}
		defer clear(completion)
		flattened, err := tooltext.FlattenCompletion(completion)
		if err != nil {
			return err
		}
		defer clear(flattened)
		content, finish, err := tooltext.CompletionToStreamFrames(flattened)
		if err != nil {
			return err
		}
		defer clear(content)
		defer clear(finish)
		for _, data := range [][]byte{content, finish} {
			if len(data) == 0 {
				continue
			}
			frame := append(append([]byte("data: "), data...), '\n', '\n')
			_, err = s.write(frame)
			clear(frame)
			if err != nil {
				return err
			}
		}
		if len(s.usage) > 0 {
			if _, err = s.write(s.usage); err != nil {
				return err
			}
		}
	}
	if _, err := s.write([]byte("data: [DONE]\n\n")); err != nil {
		return err
	}
	if err := http.NewResponseController(s.writer).Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}
