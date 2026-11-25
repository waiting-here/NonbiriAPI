package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing/fstest"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/runner"
)

type lineReader struct{ *bufio.Reader }

func newLineReader(r io.Reader) *lineReader { return &lineReader{bufio.NewReader(r)} }
func (r *lineReader) line(limit int) ([]byte, error) {
	var result []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(part) > limit-len(result) {
			return nil, runner.ErrProgram
		}
		result = append(result, part...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return result, err
		}
	}
}

// The launcher relays stdin into a separate process group. Closing the pipe
// therefore cancels the guest even when the launcher itself has exited.
func watchPeer(ctx context.Context, cancel context.CancelFunc, host *lineReader) func() ([]byte, error) {
	frames := make(chan []byte)
	go func() {
		defer close(frames)
		for {
			b, err := host.line(runner.HistoryLimit + 1)
			if err != nil {
				cancel()
				return
			}
			select {
			case frames <- b:
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() ([]byte, error) {
		select {
		case b, ok := <-frames:
			if !ok {
				return nil, io.EOF
			}
			return b, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func runModule(ctx context.Context, job runner.Job, receive func() ([]byte, error), out io.Writer) error {
	if len(job.Module) > runner.ModuleLimit || len(job.Request) > runner.RequestLimit || len(job.Catalog) > runner.RequestLimit {
		return runner.ErrProgram
	}
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter().WithMemoryLimitPages(2048).WithCloseOnContextDone(true))
	defer rt.Close(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return err
	}
	send := json.NewEncoder(out)
	var cached []byte
	history := rt.NewHostModuleBuilder("nonbiri_history")
	history.NewFunctionBuilder().WithFunc(func(_ context.Context, module api.Module, opPtr, opLen, argPtr, argLen uint32) int32 {
		cached = nil
		if opLen > 16 || argLen > runner.ReplyLimit {
			return -1
		}
		op, ok := module.Memory().Read(opPtr, opLen)
		if !ok {
			return -1
		}
		args, ok := module.Memory().Read(argPtr, argLen)
		if !ok || !json.Valid(args) {
			return -1
		}
		if err := send.Encode(runner.Message{Operation: string(op), Args: json.RawMessage(args)}); err != nil {
			return -1
		}
		body, err := receive()
		if err != nil {
			return -1
		}
		cached = bytes.TrimSpace(body)
		return int32(len(cached))
	}).Export("query")
	history.NewFunctionBuilder().WithFunc(func(_ context.Context, module api.Module, ptr, size uint32) int32 {
		if cached == nil || len(cached) != int(size) || !module.Memory().Write(ptr, cached) {
			return -1
		}
		cached = nil
		return int32(size)
	}).Export("read")
	if _, err := history.Instantiate(ctx); err != nil {
		return err
	}
	code, err := rt.CompileModule(ctx, job.Module)
	if err != nil {
		return err
	}
	defer code.Close(ctx)
	files := fstest.MapFS{"catalog.json": {Data: job.Catalog, Mode: 0444}}
	var result = runner.Buffer{Limit: runner.ReplyLimit}
	cfg := wazero.NewModuleConfig().WithName("").WithStdin(bytes.NewReader(job.Request)).WithStdout(&result).WithStderr(io.Discard).WithSysNanotime().WithFSConfig(wazero.NewFSConfig().WithFSMount(files, "/rules"))
	module, err := rt.InstantiateModule(ctx, code, cfg)
	if module != nil {
		defer module.Close(ctx)
	}
	var exit *sys.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 0) {
		return err
	}
	if result.Overflow || !json.Valid(result.Bytes.Bytes()) {
		return runner.ErrProgram
	}
	return send.Encode(runner.Message{Reply: json.RawMessage(result.Bytes.Bytes())})
}
