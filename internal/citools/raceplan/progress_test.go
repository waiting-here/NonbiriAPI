package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestGroupProgressIsVisibleBeforeCommandFinishes(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	written := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- executeGroups([]commandGroup{{Label: "example/package group 1/1"}}, 1, func(_ commandGroup, stream io.Writer) error {
			// Hide Reader's WriterTo to exercise the same io.Copy path as exec.
			_, err := io.Copy(stream, struct{ io.Reader }{strings.NewReader("=== RUN   TestCurrent\n")})
			close(written)
			<-release
			return err
		}, &output)
	}()
	<-written
	visible := output.String()
	if !strings.Contains(visible, "raceplan: progress example/package group 1/1: === RUN   TestCurrent\n") {
		t.Errorf("test start is not visible before command completion: %q", visible)
	}
	if strings.Contains(visible, "raceplan: output") {
		t.Errorf("final output block appeared before command completion: %q", visible)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "raceplan: output example/package group 1/1\n=== RUN   TestCurrent\n") {
		t.Fatalf("final package block changed: %q", output.String())
	}
}

func TestProgressOutputRetainsChunksAndOnlyReportsTopLevelEvents(t *testing.T) {
	t.Parallel()
	const original = "=== RUN   TestTop\r\n=== RUN   TestTop/sub\n--- PASS: TestTop (0.12s)\n    --- PASS: TestTop/sub (0.10s)\nlog without final newline"
	var output bytes.Buffer
	var outputMu sync.Mutex
	stream := progressOutput{label: "example/package", output: &output, outputMu: &outputMu}
	for _, chunk := range []string{original[:5], original[5:19], original[19:48], original[48:]} {
		if n, err := stream.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write %d bytes: n=%d err=%v", len(chunk), n, err)
		}
	}
	if string(stream.Bytes()) != original {
		t.Fatalf("buffered output changed: %q", stream.Bytes())
	}
	want := "raceplan: progress example/package: === RUN   TestTop\n" +
		"raceplan: progress example/package: --- PASS: TestTop (0.12s)\n"
	if output.String() != want {
		t.Fatalf("progress = %q, want %q", output.String(), want)
	}
}

func TestCatalogProgressNamesEachPackageAndRetainsFailure(t *testing.T) {
	t.Parallel()
	listed := []listedPackage{
		{ImportPath: "example/first", TestGoFiles: []string{"test.go"}, Dir: t.TempDir()},
		{ImportPath: "example/second", TestGoFiles: []string{"test.go"}, Dir: t.TempDir()},
	}
	for _, item := range listed {
		if err := os.WriteFile(filepath.Join(item.Dir, "test.go"), []byte("package example\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hints := timingHints{SplitPackages: []string{"example/first", "example/second"}}
	var output bytes.Buffer
	sentinel := errors.New("listing failed")
	var calls []string
	_, err := discoverCatalog(listed, hints, &output, func(packagePath string) ([]string, error) {
		calls = append(calls, packagePath)
		if !strings.Contains(output.String(), "raceplan: catalog listing "+packagePath+"\n") {
			t.Errorf("missing start before listing %s: %q", packagePath, output.String())
		}
		if packagePath == "example/second" {
			return nil, sentinel
		}
		return []string{"TestOne"}, nil
	})
	if !errors.Is(err, sentinel) || len(calls) != 2 {
		t.Fatalf("catalog calls=%v err=%v", calls, err)
	}
	for _, packagePath := range calls {
		if !strings.Contains(output.String(), "raceplan: catalog finished "+packagePath+" in ") {
			t.Errorf("missing elapsed time for %s: %q", packagePath, output.String())
		}
	}
}
