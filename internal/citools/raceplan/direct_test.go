package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestDirectRunnerPreservesAllArgumentsAndCleansTemplate(t *testing.T) {
	t.Parallel()
	fixture := directFixtureForTest(t)
	args := []string{"-p", "1", "-run", "^(TestFirst|TestSecond/sub)$", "./internal/auth", "./internal/game/ranking", "-args", "-custom=value with spaces"}
	var output bytes.Buffer
	prepared, ran := 0, 0
	runner := directRunner{
		prepare: func(goTool, identity string) (runnerFixture, error) {
			prepared++
			if goTool != "exact-go-tool" || identity != "explicit" {
				t.Errorf("prepare arguments: %q %q", goTool, identity)
			}
			return fixture, nil
		},
		run: func(goTool string, actual, environment []string, stdout, stderr io.Writer) error {
			ran++
			want := append([]string{"test", "-v", "-race", "-shuffle=on", "-count=1", "-timeout=5m"}, args...)
			if goTool != "exact-go-tool" || !reflect.DeepEqual(actual, want) {
				t.Errorf("test command = %q %q, want %q", goTool, actual, want)
			}
			if !reflect.DeepEqual(environment, fixture.childEnvironment([]string{"UNRELATED=kept"})) {
				t.Errorf("child environment = %q", environment)
			}
			if _, err := os.Stat(fixture.path); err != nil {
				t.Errorf("template missing before child runs: %v", err)
			}
			return nil
		},
	}
	if err := runner.execute("exact-go-tool", "5m", args, []string{"UNRELATED=kept"}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if prepared != 1 || ran != 1 {
		t.Fatalf("prepare calls=%d test calls=%d", prepared, ran)
	}
	if _, err := os.Stat(fixture.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned template directory was not removed: %v", err)
	}
}

func TestDirectRunnerDoesNotReplaceInheritedTemplateErrors(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{raceTemplatePathEnv + "="},
		{raceTemplateSHAEnv + "=invalid"},
		{raceTemplateIDEnv + "=wrong-run"},
		{raceTemplatePathEnv + "=missing", raceTemplateSHAEnv + "=bad", raceTemplateIDEnv + "=wrong"},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, []string{strings.ToLower(raceTemplatePathEnv) + "="})
	}
	for _, environment := range cases {
		sentinel := errors.New("inherited template rejected by child")
		runner := directRunner{
			prepare: func(string, string) (runnerFixture, error) {
				t.Error("rebuilt an inherited template")
				return runnerFixture{}, errors.New("unexpected template build")
			},
			run: func(_ string, _, actual []string, _, _ io.Writer) error {
				if !reflect.DeepEqual(actual, environment) {
					t.Errorf("inherited environment changed: %q, want %q", actual, environment)
				}
				return sentinel
			},
		}
		if err := runner.execute("go", "5m", []string{"./internal/auth"}, environment, io.Discard, io.Discard); !errors.Is(err, sentinel) {
			t.Errorf("inherited failure was lost: %v", err)
		}
	}
}

func TestDirectRunnerKeepsChildExitCodeAndCleansAfterFailure(t *testing.T) {
	t.Parallel()
	fixture := directFixtureForTest(t)
	var output bytes.Buffer
	runner := directRunner{
		prepare: func(string, string) (runnerFixture, error) { return fixture, nil },
		run: func(_ string, _, environment []string, stdout, stderr io.Writer) error {
			child := exec.Command(os.Args[0], "-test.run=^TestRaceCommandExitHelper$")
			child.Env = append(environment, "NONBIRI_RACEPLAN_TEST_EXIT=23", "NONBIRI_RACEPLAN_TEST_LABEL=direct-child")
			child.Stdout, child.Stderr = stdout, stderr
			return child.Run()
		},
	}
	err := runner.execute("go", "5m", []string{"./internal/auth"}, withoutRaceTemplateEnvironment(os.Environ()), &output, &output)
	if commandExitCode(err) != 23 {
		t.Fatalf("child status=%d err=%v output=%q", commandExitCode(err), err, output.String())
	}
	if _, err := os.Stat(fixture.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed child's template directory remained: %v", err)
	}
}

func TestDirectRunnerStopsWhenTemplateBuildFails(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("builder failed")
	runner := directRunner{
		prepare: func(string, string) (runnerFixture, error) { return runnerFixture{}, sentinel },
		run: func(string, []string, []string, io.Writer, io.Writer) error {
			t.Error("ran tests after failed template preparation")
			return nil
		},
	}
	if err := runner.execute("go", "5m", []string{"./internal/auth"}, nil, io.Discard, io.Discard); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err := runner.execute("go", "5m", nil, nil, io.Discard, io.Discard); err == nil {
		t.Error("accepted empty direct arguments")
	}
	if err := runCLI([]string{"-direct", "-shard", "1/1", "--", "./internal/auth"}, io.Discard, io.Discard); err == nil {
		t.Error("accepted direct mode with a catalog selection")
	}
}

func directFixtureForTest(t *testing.T) runnerFixture {
	t.Helper()
	dir, err := os.MkdirTemp(t.TempDir(), "owned-template-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strings.Repeat("a", 64)+".sqlite")
	if err := os.WriteFile(path, []byte("synthetic test template"), 0o400); err != nil {
		t.Fatal(err)
	}
	return runnerFixture{dir: dir, path: path, identity: strings.Repeat("a", 64), sha256: strings.Repeat("b", 64), size: 23}
}
