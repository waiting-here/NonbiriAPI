package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Client struct{ Path string }

func Configured() Client {
	if !UploadsEnabled {
		return Client{}
	}
	path := os.Getenv("NONBIRI_COMPANION_WORKER")
	if !filepath.IsAbs(path) {
		return Client{}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Client{}
	}
	return Client{Path: path}
}

func (c Client) Compile(ctx context.Context, source string) ([]byte, string, error) {
	if !UploadsEnabled {
		return nil, "", ErrUploadsDisabled
	}
	if c.Path == "" {
		return nil, "", ErrWorker
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Path, "compile")
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(source)
	code, diagnostic := Buffer{Limit: ModuleLimit}, Buffer{Limit: DiagnosticLimit}
	cmd.Stdout, cmd.Stderr = &code, &diagnostic
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 2 {
			return nil, strings.ToValidUTF8(diagnostic.Bytes.String(), "�"), ErrProgram
		}
		return nil, "", ErrWorker
	}
	if code.Overflow || !bytes.HasPrefix(code.Bytes.Bytes(), []byte{0, 'a', 's', 'm'}) {
		return nil, "", ErrWorker
	}
	return code.Bytes.Bytes(), "", nil
}

type History func(context.Context, string, json.RawMessage) (json.RawMessage, error)

func (c Client) Decide(ctx context.Context, job Job, history History) (json.RawMessage, error) {
	if !UploadsEnabled {
		return nil, ErrUploadsDisabled
	}
	if c.Path == "" {
		return nil, ErrWorker
	}
	if len(job.Module) > ModuleLimit || len(job.Request) > RequestLimit || len(job.Catalog) > RequestLimit {
		return nil, ErrProgram
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Path, "run")
	cmd.WaitDelay = 2 * time.Second
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	// The worker watches stdin across the systemd boundary. Give it time to
	// cancel its guest before CommandContext force-stops the local launcher.
	cmd.Cancel = input.Close
	if err = cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, ErrWorker
	}
	// Always close pipes and reap before returning the slot to the host.
	defer func() { _ = input.Close(); _ = output.Close(); _ = cmd.Wait() }()
	encoder := json.NewEncoder(input)
	job.WatchPeer = true
	if err = encoder.Encode(job); err != nil {
		return nil, ErrProgram
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), ReplyLimit*2)
	for scanner.Scan() {
		var msg Message
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			return nil, ErrProgram
		}
		if len(msg.Reply) > 0 && msg.Operation == "" {
			if len(msg.Reply) > ReplyLimit {
				return nil, ErrProgram
			}
			return msg.Reply, nil
		}
		switch msg.Operation {
		case "list", "read", "view_at", "aggregate":
		default:
			return nil, ErrProgram
		}
		var data json.RawMessage
		var historyErr error
		if history != nil {
			data, historyErr = history(ctx, msg.Operation, msg.Args)
		}
		if historyErr != nil || len(data) == 0 {
			data = json.RawMessage(`{"error":"unavailable"}`)
			if historyErr != nil {
				switch historyErr.Error() {
				case "expired", "budget_exhausted", "incompatible_rules":
					data, _ = json.Marshal(map[string]string{"error": historyErr.Error()})
				}
			}
		}
		if len(data) > HistoryLimit || !json.Valid(data) {
			return nil, ErrProgram
		}
		if err = encoder.Encode(data); err != nil {
			return nil, ErrProgram
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if scanner.Err() != nil && !errors.Is(scanner.Err(), io.EOF) {
		return nil, ErrProgram
	}
	return nil, ErrProgram
}
