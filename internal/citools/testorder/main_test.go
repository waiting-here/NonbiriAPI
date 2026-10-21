package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeHints(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "timings.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const validHints = `{
	"version": 1,
	"default_package_seconds": 5,
	"package_seconds": {}
}`

func TestRunPreservesCompleteLiveListAndStableWeights(t *testing.T) {
	path := writeHints(t, `{
		"version": 1,
		"default_package_seconds": 5,
		"package_seconds": {
			"example/zero-a": 0,
			"example/zero-b": 0,
			"example/fast": 1,
			"example/equal-a": 5,
			"example/equal-b": 5,
			"example/slow": 9,
			"example/removed": 99
		},
		"source": {"measurement": "ordinary tests"}
	}`)
	input := "example/zero-b\nexample/new-b example/fast\r\nexample/new-a\texample/equal-b\nexample/slow\nexample/equal-a\nexample/zero-a\n"
	var output bytes.Buffer
	if err := run([]string{"-timings", path}, strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"example/slow", "example/new-b", "example/new-a", "example/equal-b",
		"example/equal-a", "example/fast", "example/zero-b", "example/zero-a",
	}
	if output.String() != strings.Join(want, "\n")+"\n" {
		t.Fatalf("ordered packages = %q, want %q", output.String(), want)
	}
	gotSet, inputSet := strings.Fields(output.String()), strings.Fields(input)
	slices.Sort(gotSet)
	slices.Sort(inputSet)
	if !slices.Equal(gotSet, inputSet) {
		t.Fatalf("output is not a complete input permutation: %v != %v", gotSet, inputSet)
	}
}

func TestRunRetainsAllUnknownPackages(t *testing.T) {
	path := writeHints(t, validHints)
	var output bytes.Buffer
	if err := run([]string{"-timings", path}, strings.NewReader("new/z new/a new/m"), &output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "new/z\nnew/a\nnew/m\n" {
		t.Fatalf("unknown package order changed: %q", got)
	}
}

func TestRunRejectsInvalidHintsWithoutOutput(t *testing.T) {
	for name, content := range map[string]string{
		"malformed":          "{",
		"null document":      "null",
		"wrong version":      `{"version":2,"default_package_seconds":5,"package_seconds":{}}`,
		"missing default":    `{"version":1,"package_seconds":{}}`,
		"zero default":       `{"version":1,"default_package_seconds":0,"package_seconds":{}}`,
		"negative default":   `{"version":1,"default_package_seconds":-1,"package_seconds":{}}`,
		"null default":       `{"version":1,"default_package_seconds":null,"package_seconds":{}}`,
		"infinite default":   `{"version":1,"default_package_seconds":1e999,"package_seconds":{}}`,
		"missing packages":   `{"version":1,"default_package_seconds":5}`,
		"null packages":      `{"version":1,"default_package_seconds":5,"package_seconds":null}`,
		"array packages":     `{"version":1,"default_package_seconds":5,"package_seconds":[]}`,
		"negative seconds":   `{"version":1,"default_package_seconds":5,"package_seconds":{"example/a":-1}}`,
		"null seconds":       `{"version":1,"default_package_seconds":5,"package_seconds":{"example/a":null}}`,
		"infinite seconds":   `{"version":1,"default_package_seconds":5,"package_seconds":{"example/a":1e999}}`,
		"nonnumeric seconds": `{"version":1,"default_package_seconds":5,"package_seconds":{"example/a":"5"}}`,
		"empty package":      `{"version":1,"default_package_seconds":5,"package_seconds":{"":1}}`,
		"spaced package":     `{"version":1,"default_package_seconds":5,"package_seconds":{"example/a b":1}}`,
		"pattern package":    `{"version":1,"default_package_seconds":5,"package_seconds":{"example/...":1}}`,
		"flag package":       `{"version":1,"default_package_seconds":5,"package_seconds":{"-race":1}}`,
		"unknown field":      `{"version":1,"default_package_seconds":5,"package_seconds":{},"misspelled":5}`,
		"multiple values":    validHints + "\n{}",
		"trailing garbage":   validHints + "\ngarbage",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeHints(t, content)
			var output bytes.Buffer
			if err := run([]string{"-timings", path}, strings.NewReader("example/a"), &output); err == nil {
				t.Fatal("invalid configuration succeeded")
			}
			if output.Len() != 0 {
				t.Fatalf("invalid configuration emitted packages: %q", output.String())
			}
		})
	}
}

func TestRunRejectsInvalidInputWithoutOutput(t *testing.T) {
	path := writeHints(t, validHints)
	for name, input := range map[string]string{
		"empty":      "",
		"whitespace": " \t\r\n",
		"duplicate":  "example/a\nexample/b\nexample/a\n",
		"pattern":    "example/a\nexample/...",
		"option":     "example/a\n-race",
		"backslash":  "example\a",
		"nul":        "example/a\x00",
		"long token": strings.Repeat("a", 70_000),
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := run([]string{"-timings", path}, strings.NewReader(input), &output); err == nil {
				t.Fatal("invalid input succeeded")
			}
			if output.Len() != 0 {
				t.Fatalf("invalid input emitted a partial package list: %q", output.String())
			}
		})
	}
}

func TestRunReportsFlagAndFileErrors(t *testing.T) {
	path := writeHints(t, validHints)
	for _, args := range [][]string{
		{"-unknown"},
		{"-timings"},
		{"-timings", path, "unexpected"},
		{"-timings", filepath.Join(t.TempDir(), "absent.json")},
	} {
		var output bytes.Buffer
		if err := run(args, strings.NewReader("example/a"), &output); err == nil {
			t.Fatalf("invalid arguments succeeded: %v", args)
		}
		if output.Len() != 0 {
			t.Fatalf("invalid arguments emitted packages: %q", output.String())
		}
	}
}

type failingIO struct{ err error }

func (f failingIO) Read([]byte) (int, error)  { return 0, f.err }
func (f failingIO) Write([]byte) (int, error) { return 0, f.err }

func TestRunPropagatesInputAndOutputErrors(t *testing.T) {
	path := writeHints(t, validHints)
	failure := errors.New("I/O failure")
	var output bytes.Buffer
	reader := io.MultiReader(strings.NewReader("example/a\n"), failingIO{failure})
	if err := run([]string{"-timings", path}, reader, &output); !errors.Is(err, failure) {
		t.Fatalf("read error = %v, want underlying failure", err)
	}
	if output.Len() != 0 {
		t.Fatalf("read failure emitted a partial package list: %q", output.String())
	}
	if err := run([]string{"-timings", path}, strings.NewReader("example/a"), failingIO{failure}); !errors.Is(err, failure) {
		t.Fatalf("write error = %v, want underlying failure", err)
	}
}
