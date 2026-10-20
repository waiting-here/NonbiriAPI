package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/upgrade/v1"
)

func run(ctx context.Context, args []string, output io.Writer) error {
	return runWithProgress(ctx, args, output, io.Discard)
}

func runWithProgress(ctx context.Context, args []string, output, progress io.Writer) error {
	flags := flag.NewFlagSet("nonbiri-upgrade-v1", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	schemaInfo := flags.Bool("schema-info", false, "print compiled conversion identities")
	jsonOutput := flags.Bool("json", false, "print JSON metadata")
	check := flags.Bool("check", false, "audit the stopped database without writing")
	apply := flags.Bool("apply", false, "convert the stopped database using the existing recovery snapshot")
	path := flags.String("database", os.Getenv("NONBIRI_DB_PATH"), "database file")
	timeout := flags.Duration("timeout", 30*time.Minute, "conversion deadline")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid conversion arguments")
	}
	if *schemaInfo {
		if !*jsonOutput || *check || *apply || flags.NArg() != 0 {
			return errors.New("usage: nonbiri-upgrade-v1 --schema-info --json")
		}
		return json.NewEncoder(output).Encode(v1.SchemaDescriptor())
	}
	if *jsonOutput {
		return errors.New("--json requires --schema-info")
	}
	if *check == *apply || *path == "" || flags.NArg() != 0 || *timeout <= 0 {
		return errors.New("usage: nonbiri-upgrade-v1 {--check|--apply} --database FILE [--timeout 30m]")
	}
	vault, err := config.LoadSecretVault()
	if err != nil {
		return err
	}
	defer vault.Close()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	encoder := json.NewEncoder(progress)
	result, err := v1.Run(ctx, *path, vault, v1.Options{Apply: *apply, LegacyGatewayModels: os.Getenv("NONBIRI_GATEWAY_MODEL_CAPABILITIES"), Progress: func(event v1.Progress) {
		// Losing a progress reader does not cancel the owned conversion.
		_ = encoder.Encode(event)
	}})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func main() {
	// Writes to a disconnected stderr pipe must return errors instead of
	// terminating the conversion before its transaction completes.
	signal.Ignore(syscall.SIGPIPE)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runWithProgress(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
