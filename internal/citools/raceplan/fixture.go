package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	raceTemplatePathEnv = "NONBIRI_RACE_TEMPLATE_PATH"
	raceTemplateSHAEnv  = "NONBIRI_RACE_TEMPLATE_SHA256"
	raceTemplateIDEnv   = "NONBIRI_RACE_TEMPLATE_ID"
)

type runnerFixture struct {
	dir       string
	path      string
	identity  string
	sha256    string
	size      int
	buildTime time.Duration
}

func prepareRunnerFixture(goTool, planDigest string) (result runnerFixture, resultErr error) {
	started := time.Now()
	dir, err := os.MkdirTemp("", "nonbiri-race-template-")
	if err != nil {
		return runnerFixture{}, fmt.Errorf("create race template directory: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.RemoveAll(dir))
		}
	}()
	if err := os.Chmod(dir, 0o700); err != nil {
		return runnerFixture{}, fmt.Errorf("secure race template directory: %w", err)
	}
	root, err := moduleRoot()
	if err != nil {
		return runnerFixture{}, err
	}
	buildPath := filepath.Join(dir, "template.sqlite")
	command := exec.Command(goTool, "run", "-race", "./internal/citools/racefixture", "-output", buildPath)
	command.Dir = root
	command.Env = withoutRaceTemplateEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		return runnerFixture{}, fmt.Errorf("build race-instrumented template: %w: %s", err, strings.TrimSpace(string(output)))
	}
	info, err := os.Lstat(buildPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		(runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		return runnerFixture{}, fmt.Errorf("builder did not produce a private regular template: %v", err)
	}
	image, err := os.ReadFile(buildPath)
	if err != nil {
		return runnerFixture{}, fmt.Errorf("read built race template: %w", err)
	}
	if len(image) < 100 {
		return runnerFixture{}, errors.New("built race template is too short")
	}
	imageHash := sha256.Sum256(image)
	sha := hex.EncodeToString(imageHash[:])
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return runnerFixture{}, fmt.Errorf("create race template run identity: %w", err)
	}
	identityInput := sha256.New()
	fmt.Fprintf(identityInput, "race-template-v1\nplan=%s\nsha256=%s\n", planDigest, sha)
	_, _ = identityInput.Write(nonce[:])
	identity := hex.EncodeToString(identityInput.Sum(nil))
	finalPath := filepath.Join(dir, identity+".sqlite")
	if err := os.Rename(buildPath, finalPath); err != nil {
		return runnerFixture{}, fmt.Errorf("name race template for this run: %w", err)
	}
	if err := os.Chmod(finalPath, 0o400); err != nil {
		return runnerFixture{}, fmt.Errorf("make race template read-only: %w", err)
	}
	return runnerFixture{dir: dir, path: finalPath, identity: identity, sha256: sha, size: len(image), buildTime: time.Since(started)}, nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate race fixture module: %w", err)
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.HasPrefix(string(data), "module github.com/waiting-here/NonbiriAPI\n") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("cannot locate NonbiriAPI module for race fixture builder")
		}
		dir = parent
	}
}

func (fixture runnerFixture) childEnvironment(parent []string) []string {
	return append(withoutRaceTemplateEnvironment(parent),
		raceTemplatePathEnv+"="+fixture.path,
		raceTemplateSHAEnv+"="+fixture.sha256,
		raceTemplateIDEnv+"="+fixture.identity)
}

func (fixture runnerFixture) cleanup() error {
	var unlockErr error
	if runtime.GOOS == "windows" {
		unlockErr = os.Chmod(fixture.path, 0o600)
	}
	return errors.Join(unlockErr, os.RemoveAll(fixture.dir))
}

func withoutRaceTemplateEnvironment(parent []string) []string {
	filtered := make([]string, 0, len(parent))
	for _, item := range parent {
		if strings.HasPrefix(item, raceTemplatePathEnv+"=") ||
			strings.HasPrefix(item, raceTemplateSHAEnv+"=") ||
			strings.HasPrefix(item, raceTemplateIDEnv+"=") {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}
