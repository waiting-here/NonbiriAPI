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
	"github.com/waiting-here/NonbiriAPI/internal/upgrade/rc7"
)

func run(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("nonbiri-upgrade-rc7", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	check := flags.Bool("check", false, "check the stopped database without writing")
	apply := flags.Bool("apply", false, "convert the stopped database using the existing recovery snapshot")
	path := flags.String("database", os.Getenv("NONBIRI_DB_PATH"), "database file")
	timeout := flags.Duration("timeout", 30*time.Minute, "conversion deadline")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid conversion arguments")
	}
	if *check == *apply || *path == "" || flags.NArg() != 0 || *timeout <= 0 {
		return errors.New("usage: nonbiri-upgrade-rc7 {--check|--apply} --database FILE [--timeout 30m]")
	}
	vault, err := config.LoadSecretVault()
	if err != nil {
		return err
	}
	defer vault.Close()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	result, err := rc7.Run(ctx, *path, vault, rc7.Options{Apply: *apply, LegacyGatewayModels: os.Getenv("NONBIRI_GATEWAY_MODEL_CAPABILITIES")})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
