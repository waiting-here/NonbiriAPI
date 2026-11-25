// The optional worker compiles C++17 inside a rootless container and executes
// a WASI module in a separate, time-limited interpreter process.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/runner"
)

func main() {
	if !runner.UploadsEnabled {
		fmt.Fprintln(os.Stderr, runner.ErrUploadsDisabled)
		os.Exit(3)
	}
	if len(os.Args) != 2 {
		os.Exit(3)
	}
	var err error
	switch os.Args[1] {
	case "compile":
		err = compile()
	case "run":
		debug.SetMemoryLimit(220 << 20)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// Limit only the initial message. The same stdin then receives history
		// answers, which are bounded separately for each request.
		reader := newLineReader(os.Stdin)
		initial, readErr := reader.line(32 << 20)
		var job runner.Job
		if readErr != nil || json.Unmarshal(initial, &job) != nil {
			os.Exit(2)
		}
		next := func() ([]byte, error) { return reader.line(runner.HistoryLimit + 1) }
		if job.WatchPeer {
			next = watchPeer(ctx, cancel, reader)
		}
		err = runModule(ctx, job, next, os.Stdout)
	default:
		os.Exit(3)
	}
	if err != nil {
		var source compileError
		if errors.As(err, &source) {
			fmt.Fprint(os.Stderr, string(source))
			os.Exit(2)
		}
		if !errors.Is(err, io.EOF) {
			fmt.Fprintln(os.Stderr, "companion worker could not finish the job")
		}
		os.Exit(3)
	}
}
