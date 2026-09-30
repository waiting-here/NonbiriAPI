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
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/fatfish"
)

// Maintenance opens the configured database without listeners or workers.
// Operators stop the service and preserve a consistent recovery set first.
func runMaintenance(args []string, output io.Writer) (result error) {
	if len(args) < 2 || args[0] != "maintenance" || (args[1] != "fatfish-cleanup-plan" && args[1] != "fatfish-cleanup") {
		return errors.New("usage: nonbiriapi maintenance {fatfish-cleanup-plan|fatfish-cleanup} --source FILE [--manifest FILE --operation-key KEY]")
	}
	apply := args[1] == "fatfish-cleanup"
	flags := flag.NewFlagSet(args[1], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourcePath := flags.String("source", "", "trusted source identity JSON")
	manifestPath := flags.String("manifest", "", "approved cleanup manifest JSON")
	key := flags.String("operation-key", "", "stable cleanup operation key")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *sourcePath == "" || (apply && (*manifestPath == "" || *key == "")) || (!apply && (*manifestPath != "" || *key != "")) {
		return errors.New("provide source identity; cleanup also requires its manifest and operation key")
	}
	var source fatfish.CleanupSource
	if err := readMaintenanceJSON(*sourcePath, 4096, &source); err != nil {
		return fmt.Errorf("read cleanup source: %w", err)
	}
	var manifest fatfish.CleanupManifest
	if apply {
		if err := readMaintenanceJSON(*manifestPath, 256<<20, &manifest); err != nil {
			return fmt.Errorf("read cleanup manifest: %w", err)
		}
		if manifest.Source != source {
			return errors.New("cleanup source differs from the approved manifest")
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	vault := cfg.TakeSecretVault()
	if vault == nil {
		return errors.New("secret vault initialization failed")
	}
	defer vault.Close()
	info, err := os.Stat(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("maintenance requires an existing database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("maintenance requires a regular database file")
	}
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalContext, cfg.StartupTimeout)
	defer cancel()
	store, err := db.OpenContext(ctx, cfg.DBPath, vault)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, store.Close()) }()
	encoder := json.NewEncoder(output)
	if !apply {
		plan, err := fatfish.PlanLegacyCleanup(ctx, store.DB(), source)
		if err != nil {
			return err
		}
		return encoder.Encode(plan)
	}
	receipt, err := fatfish.RunOfflineLegacyCleanup(ctx, store.DB(), source, manifest, *key, time.Now())
	if err != nil {
		return err
	}
	return encoder.Encode(receipt)
}

func readMaintenanceJSON(path string, limit int64, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	input, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return err
	}
	if int64(len(input)) > limit {
		return errors.New("maintenance input exceeds its size limit")
	}
	return json.Unmarshal(input, target)
}
