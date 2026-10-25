package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type directRunner struct {
	prepare func(string, string) (runnerFixture, error)
	run     func(string, []string, []string, io.Writer, io.Writer) error
}

func executeDirect(goTool, timeout string, args []string, stdout, stderr io.Writer) error {
	runner := directRunner{
		prepare: prepareRunnerFixture,
		run: func(goTool string, args, environment []string, stdout, stderr io.Writer) error {
			command := exec.Command(goTool, args...)
			command.Env = environment
			command.Stdout, command.Stderr = stdout, stderr
			return command.Run()
		},
	}
	return runner.execute(goTool, timeout, args, os.Environ(), stdout, stderr)
}

func (runner directRunner) execute(goTool, timeout string, args, environment []string, stdout, stderr io.Writer) (resultErr error) {
	if len(args) == 0 {
		return errors.New("direct execution requires go test arguments")
	}
	if duration, err := time.ParseDuration(timeout); err != nil || duration <= 0 {
		return fmt.Errorf("invalid timeout %q", timeout)
	}
	// Any inherited template setting belongs to the caller. Incomplete or invalid
	// settings must reach dbfixture unchanged so its validation cannot be bypassed.
	if hasTemplateEnvironment(environment) {
		fmt.Fprintln(stderr, "raceplan: explicit tests retain inherited template environment")
	} else {
		fixture, err := runner.prepare(goTool, "explicit")
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, fixture.cleanup()) }()
		environment = fixture.childEnvironment(environment)
		fmt.Fprintf(stderr, "raceplan: explicit-test template built once in %s bytes=%d\n", fixture.buildTime.Round(time.Millisecond), fixture.size)
	}
	commandArgs := []string{"test", "-v", "-race", "-shuffle=on", "-count=1", "-timeout=" + timeout}
	commandArgs = append(commandArgs, args...)
	return runner.run(goTool, commandArgs, environment, stdout, stderr)
}

func hasTemplateEnvironment(environment []string) bool {
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		for _, templateName := range []string{raceTemplatePathEnv, raceTemplateSHAEnv, raceTemplateIDEnv} {
			if name == templateName || (runtime.GOOS == "windows" && strings.EqualFold(name, templateName)) {
				return true
			}
		}
	}
	return false
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var child *exec.ExitError
	if errors.As(err, &child) && child.ExitCode() > 0 {
		return child.ExitCode()
	}
	return 1
}
