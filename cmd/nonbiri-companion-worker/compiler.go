package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/kit"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/runner"
)

const compilerImage = "ghcr.io/webassembly/wasi-sdk@sha256:52595085a0ffbaf574d8d678a9aa81305f0b3af81c639291f15bc7713e42848e"

type compileError string

func (e compileError) Error() string { return string(e) }

func compile() error {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		return runner.ErrWorker
	}
	source, err := io.ReadAll(io.LimitReader(os.Stdin, runner.SourceLimit+1))
	if err != nil || len(source) == 0 || len(source) > runner.SourceLimit {
		return compileError("Invalid source size")
	}
	temp, err := os.MkdirTemp("", "companion-source-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	sdk := filepath.Join(temp, "sdk")
	if err = os.Mkdir(sdk, 0755); err != nil {
		return err
	}
	if err = os.CopyFS(sdk, kit.SDK()); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(sdk, "bot.cpp"), source, 0644); err != nil {
		return err
	}
	// Only this read-only directory, containing the submission and public headers,
	// is visible from the container. No service or home directory is mounted.
	if err = fs.WalkDir(os.DirFS(sdk), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(filepath.Join(sdk, name), 0755)
		}
		return os.Chmod(filepath.Join(sdk, name), 0644)
	}); err != nil {
		return err
	}
	var nonce [12]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	name := "nonbiri-compile-" + hex.EncodeToString(nonce[:])
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// Podman is rootless under the caller's dedicated account. All compiler
	// arguments and mounts are trusted constants apart from the fixed temp path.
	cmd := exec.CommandContext(ctx, "podman", "run", "--rm", "--pull=never", "--timeout=90", "--name", name, "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65534:65534", "--memory=768m", "--memory-swap=768m", "--cpus=1", "--pids-limit=32", "--ulimit", "fsize=8388608:8388608", "--log-driver=none", "--tmpfs", "/tmp:rw,nosuid,nodev,size=96m,mode=1777", "--volume", sdk+":/sdk:ro,Z", "--workdir=/tmp", "--entrypoint=/bin/sh", compilerImage, "-ec", "/opt/wasi-sdk/bin/clang++ -std=c++17 -O2 -fno-exceptions -DJSON_NOEXCEPTION -ferror-limit=5 -I/sdk /sdk/main.cpp -o /tmp/bot.wasm && cat /tmp/bot.wasm")
	code, diag := runner.Buffer{Limit: runner.ModuleLimit}, runner.Buffer{Limit: runner.DiagnosticLimit}
	cmd.Stdout, cmd.Stderr = &code, &diag
	err = cmd.Run()
	if ctx.Err() != nil {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = exec.CommandContext(cleanup, "podman", "rm", "--force", name).Run()
		return compileError("Compilation exceeded 90 seconds")
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() >= 125 {
			return runner.ErrWorker
		}
		msg := strings.TrimSpace(diag.Bytes.String())
		if msg == "" {
			msg = "Compilation failed"
		}
		return compileError(msg)
	}
	if code.Overflow || !bytes.HasPrefix(code.Bytes.Bytes(), []byte{0, 'a', 's', 'm'}) {
		return compileError("Compiled program exceeded the module limit")
	}
	_, err = os.Stdout.Write(code.Bytes.Bytes())
	return err
}
