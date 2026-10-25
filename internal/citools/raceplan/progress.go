package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Keep the final package block intact for timing consumers while exposing the
// current top-level test before a command finishes or an outer budget stops it.
type progressOutput struct {
	buffer   bytes.Buffer
	label    string
	output   io.Writer
	outputMu *sync.Mutex
	pending  []byte
}

func (stream *progressOutput) Write(body []byte) (int, error) {
	stream.outputMu.Lock()
	defer stream.outputMu.Unlock()
	n, err := stream.buffer.Write(body)
	stream.pending = append(stream.pending, body[:n]...)
	for {
		end := bytes.IndexByte(stream.pending, '\n')
		if end < 0 {
			break
		}
		line := strings.TrimSuffix(string(stream.pending[:end]), "\r")
		stream.pending = stream.pending[end+1:]
		if topLevelProgress(line) {
			fmt.Fprintf(stream.output, "raceplan: progress %s: %s\n", stream.label, line)
		}
	}
	return n, err
}

func (stream *progressOutput) Len() int { return stream.buffer.Len() }

func (stream *progressOutput) Bytes() []byte { return stream.buffer.Bytes() }

func topLevelProgress(line string) bool {
	for _, prefix := range []string{"=== RUN   ", "=== PAUSE ", "=== CONT  ", "--- PASS: ", "--- FAIL: ", "--- SKIP: "} {
		if name, found := strings.CutPrefix(line, prefix); found {
			name, _, _ = strings.Cut(name, " ")
			return validTopLevelTestName(name)
		}
	}
	return false
}
