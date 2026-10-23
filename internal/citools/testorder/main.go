// Command testorder orders a live package list using ordinary test durations.
// Timing hints affect scheduling only; the input defines the complete test set.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"
)

type timingHints struct {
	Version               int                 `json:"version"`
	DefaultPackageSeconds float64             `json:"default_package_seconds"`
	PackageSeconds        map[string]*float64 `json:"package_seconds"`
	Source                json.RawMessage     `json:"source,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "testorder:", err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output io.Writer) error {
	flags := flag.NewFlagSet("testorder", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	timingsPath := flags.String("timings", "scripts/go-test-timings.json", "ordinary Go package timing hints")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	hints, err := loadTimings(*timingsPath)
	if err != nil {
		return err
	}
	packages, err := readPackages(input)
	if err != nil {
		return err
	}
	sort.SliceStable(packages, func(i, j int) bool {
		return hints.seconds(packages[i]) > hints.seconds(packages[j])
	})
	if _, err := io.WriteString(output, strings.Join(packages, "\n")+"\n"); err != nil {
		return fmt.Errorf("write package list: %w", err)
	}
	return nil
}

func loadTimings(path string) (timingHints, error) {
	var hints timingHints
	file, err := os.Open(path)
	if err != nil {
		return hints, fmt.Errorf("open timing hints: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&hints); err != nil {
		return hints, fmt.Errorf("decode timing hints: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return hints, fmt.Errorf("decode timing hints: %w", err)
	}
	if hints.Version != 1 {
		return hints, fmt.Errorf("unsupported timing version: %d", hints.Version)
	}
	if hints.DefaultPackageSeconds <= 0 {
		return hints, errors.New("default_package_seconds must be positive")
	}
	if hints.PackageSeconds == nil {
		return hints, errors.New("package_seconds must be an object")
	}
	for pkg, seconds := range hints.PackageSeconds {
		if !validPackage(pkg) {
			return hints, fmt.Errorf("invalid timing package: %q", pkg)
		}
		if seconds == nil || *seconds < 0 {
			return hints, fmt.Errorf("invalid package seconds for %q", pkg)
		}
	}
	return hints, nil
}

func (h timingHints) seconds(pkg string) float64 {
	if seconds, ok := h.PackageSeconds[pkg]; ok {
		return *seconds
	}
	return h.DefaultPackageSeconds
}

func readPackages(input io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(input)
	scanner.Split(bufio.ScanWords)
	var packages []string
	seen := make(map[string]bool)
	for scanner.Scan() {
		pkg := scanner.Text()
		if !validPackage(pkg) {
			return nil, fmt.Errorf("expected an explicit package path: %q", pkg)
		}
		if seen[pkg] {
			return nil, fmt.Errorf("duplicate input package: %q", pkg)
		}
		seen[pkg] = true
		packages = append(packages, pkg)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read package list: %w", err)
	}
	if len(packages) == 0 {
		return nil, errors.New("package list is empty")
	}
	return packages, nil
}

func validPackage(pkg string) bool {
	return pkg != "" && !strings.HasPrefix(pkg, "-") &&
		!strings.Contains(pkg, "...") &&
		!strings.Contains(pkg, `\`) &&
		strings.IndexFunc(pkg, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		}) < 0
}
