package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding"
	biddingconfig "github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes"
	likesconfig "github.com/waiting-here/NonbiriAPI/internal/game/likes/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func runVerification(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: nonbiriapi maintenance verify")
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
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalContext, cfg.StartupTimeout)
	defer cancel()
	if err := db.VerifyContext(ctx, cfg.DBPath, vault, func(ctx context.Context, database *sql.DB) error {
		return verifyDomains(ctx, database, vault)
	}); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(struct {
		Status string `json:"status"`
	}{Status: "verified"})
}

func verifyDomains(ctx context.Context, database *sql.DB, vault *secret.Vault) error {
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	reservations, err := ledger.RecoverNonterminal(ctx, tx)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("verify ledger: %w", err)
	}
	for _, reservation := range reservations {
		if reservation.Domain == "fishing_batch" && reservation.Rows.Decimal() != "1" {
			tx.Rollback()
			return errors.New("invalid fishing reservation")
		}
	}
	if err := donationquota.ValidateState(ctx, tx); err != nil {
		tx.Rollback()
		return fmt.Errorf("verify recurring quotas: %w", err)
	}
	if err := tx.Rollback(); err != nil {
		return err
	}
	identities, err := continuity.New(database, vault)
	if err != nil {
		return err
	}
	defer identities.Close()
	if err := identities.ValidateBindings(ctx); err != nil {
		return fmt.Errorf("verify identities: %w", err)
	}
	observations, err := observability.NewRepository(database)
	if err != nil {
		return err
	}
	if err := observations.VerifyCounters(ctx); err != nil {
		return fmt.Errorf("verify diagnostic counters: %w", err)
	}
	rules, err := likes.NewRules()
	if err != nil {
		return err
	}
	for _, audit := range []struct {
		name string
		run  func() error
	}{
		{"linklink", func() error { return linklink.VerifyPersistedState(ctx, database) }},
		{"rps", func() error { return rps.VerifyPersistedState(ctx, database) }},
		{"bidding", func() error {
			return duel.VerifyPersistedState(ctx, database, biddingconfig.Descriptor(), bidding.Rules{})
		}},
		{"likes", func() error { return duel.VerifyPersistedState(ctx, database, likesconfig.Descriptor(), rules) }},
		{"blackjack", func() error { return blackjack.VerifyPersistedState(ctx, database) }},
	} {
		if err := audit.run(); err != nil {
			return fmt.Errorf("verify %s: %w", audit.name, err)
		}
	}
	return nil
}
