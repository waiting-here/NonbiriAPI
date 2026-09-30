// Command racefixture creates one closed, current-schema SQLite template for
// raceplan's private runner directory. The builder needs no race instrumentation.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "racefixture: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("racefixture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("output", "", "private template file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*path) {
		return errors.New("-output must name one absolute file path")
	}
	parent, err := os.Lstat(filepath.Dir(*path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 ||
		(runtime.GOOS != "windows" && parent.Mode().Perm() != 0o700) {
		return fmt.Errorf("output parent is not private: %v", err)
	}
	file, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create template: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
			_ = os.Remove(*path)
		}
	}()
	image, err := dbfixture.BuildGenerationTwoTemplate()
	if err != nil {
		return fmt.Errorf("build current-schema template: %w", err)
	}
	for written := 0; written < len(image); {
		n, writeErr := file.Write(image[written:])
		if writeErr != nil {
			return fmt.Errorf("write template: %w", writeErr)
		}
		if n == 0 {
			return errors.New("write template: zero-length write")
		}
		written += n
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync template: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close template: %w", err)
	}
	keep = true
	_, err = fmt.Fprintf(output, "racefixture: built closed template (%d bytes)\n", len(image))
	return err
}
