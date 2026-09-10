package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type activityConfigAuth struct{}

func (activityConfigAuth) AuthorizeUserMutation(context.Context, *sql.Tx, int64) error { return nil }
func (activityConfigAuth) AuthorizeAdmin(context.Context, *sql.Tx, int64) error        { return nil }
func (activityConfigAuth) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error { return nil }

func TestStationBootstrapSurvivesThursdaySettlementAndRestart(t *testing.T) {
	ctx := context.Background()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "activity-config.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			_ = store.Close()
		}
	}()
	opensAt := time.Date(2027, 3, 25, 0, 0, 0, 0, time.FixedZone("Beijing", 8*3600)).Unix()
	now := opensAt - 1
	zero := db.EncodeU128(db.U128{})
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO users(
		discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
		total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
		total_unknown_usage_requests,revision,created_at,updated_at)
		VALUES('activity-config-operator','activity operator',1,?,?,?,?,?,?,?,?,?,?)`,
		zero, zero, zero, zero, zero, zero, zero, zero, now, now)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAccount(ctx, tx, adminID, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSiteTimezoneOffsetMinutes(480); err != nil {
		t.Fatal(err)
	}
	newRepository := func() *activities.Repository {
		t.Helper()
		repository, err := activities.NewRepository(activities.RepositoryConfig{
			Store: store, UserFinalAuth: activityConfigAuth{}, AdminFinalAuth: activityConfigAuth{},
			UserGate: activityConfigAuth{}, CursorKeys: vault,
			Now: func() time.Time { return time.Unix(now, 0) },
		})
		if err != nil {
			t.Fatal(err)
		}
		return repository
	}
	repository := newRepository()
	sequence := 0
	mutation := func(method, route string) activities.ControlMutation {
		sequence++
		return activities.ControlMutation{
			IdempotencyKey: fmt.Sprintf("activity-config-%08d", sequence), Method: method, Route: route,
			CanonicalBody: []byte(fmt.Sprintf(`{"sequence":%d}`, sequence)),
		}
	}
	revision := func() int64 {
		t.Helper()
		var revision int64
		if err := store.DB().QueryRow(`SELECT revision FROM config_revisions WHERE domain='activities'`).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return revision
	}
	assertStations := func(phase string) {
		t.Helper()
		t.Run(phase, func(t *testing.T) {
			cfg := testHTTPConfig()
			for _, station := range []struct {
				host, path string
				serve      func(*db.Store, http.ResponseWriter, *http.Request)
			}{
				{cfg.UserHost, "/api/config", servePublicConfig},
				{cfg.AdminHost, "/admin/api/branding", servePublicBranding},
			} {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { station.serve(store, w, r) })
				response := testHTTPResponse(t, handler, http.MethodGet, station.host, station.path)
				if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
					t.Errorf("%s: status=%d body=%s", station.path, response.Code, response.Body.String())
					continue
				}
				var values map[string]json.RawMessage
				if err := json.Unmarshal(response.Body.Bytes(), &values); err != nil || values["site_name"] == nil {
					t.Fatalf("invalid public projection: %s, err=%v", response.Body.String(), err)
				}
				if station.path == "/admin/api/branding" && len(values) != 2 {
					t.Fatalf("branding leaked configuration fields: %s", response.Body.String())
				}
			}
		})
	}
	const configRoute = "/admin/api/activities/config"
	const nextRoute = "/admin/api/activities/thursday/next"
	enabled, disabled := true, false
	if _, _, err := repository.PatchActivitiesConfig(ctx, adminID, mutation(http.MethodPatch, configRoute), activities.ActivitiesConfigPatch{
		ExpectedRevision: revision(), Thursday: &activities.ThursdayConfigPatch{Enabled: &enabled},
	}); err != nil {
		t.Fatal(err)
	}
	assertStations("staged while master disabled")
	created, _, err := repository.PutThursdayNext(ctx, adminID, mutation(http.MethodPut, nextRoute), activities.ThursdayNextMutation{
		ExpectedRevision: revision(), PeriodKey: "2027-03-25", OpensAt: opensAt,
		Entry: "0.001", PerUserLimit: 1, PumpsBP: activities.PumpsBP{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PatchActivitiesConfig(ctx, adminID, mutation(http.MethodPatch, configRoute), activities.ActivitiesConfigPatch{
		ExpectedRevision: revision(), MasterEnabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	assertStations("configured and enabled")
	now = opensAt + 86400
	for step := 0; step < 3; step++ {
		result, _, err := repository.RunSettlementStep(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !result.More {
			break
		}
	}
	assertSettled := func() {
		t.Helper()
		var state string
		var ready, finalizations int
		if err := store.DB().QueryRow(`SELECT state FROM thursday_periods WHERE id=?`, created.Value.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM thursday_periods WHERE state IN ('configured','open','settling')`).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='thursday_finalize'`).Scan(&finalizations); err != nil {
			t.Fatal(err)
		}
		if state != activities.PeriodStateSettled || ready != 0 || finalizations != 1 {
			t.Fatalf("state=%s ready=%d finalizations=%d", state, ready, finalizations)
		}
	}
	assertSettled()
	assertStations("last period settled")
	if err := store.SetSiteConfigValue("site_name", "Thursday test site"); err != nil {
		t.Errorf("unrelated site config update after settlement: %v", err)
	}
	beforeRestart := revision()
	for restart := 0; restart < 2; restart++ {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err = db.Open(path, vault)
		if err != nil {
			t.Fatalf("restart %d after settlement: %v", restart, err)
		}
		repository = newRepository()
		assertSettled()
		config, err := repository.GetActivitiesConfig(ctx)
		if err != nil || !config.MasterEnabled || !config.Thursday.Enabled || revision() != beforeRestart {
			t.Fatalf("restart changed activity flags/revision: %+v err=%v", config, err)
		}
		assertStations(fmt.Sprintf("restart %d", restart))
	}
	if _, _, err := repository.PatchActivitiesConfig(ctx, adminID, mutation(http.MethodPatch, configRoute), activities.ActivitiesConfigPatch{
		ExpectedRevision: revision(), MasterEnabled: &disabled,
	}); err != nil {
		t.Fatal(err)
	}
	assertStations("master paused with Thursday flag preserved")
	beforeRejected := revision()
	if _, _, err := repository.PatchActivitiesConfig(ctx, adminID, mutation(http.MethodPatch, configRoute), activities.ActivitiesConfigPatch{
		ExpectedRevision: beforeRejected, MasterEnabled: &enabled,
	}); !errors.Is(err, activities.ErrInvalidRequest) || revision() != beforeRejected {
		t.Fatalf("enable without next period must reject without revision change: %v", err)
	}
	if _, _, err := repository.PutThursdayNext(ctx, adminID, mutation(http.MethodPut, nextRoute), activities.ThursdayNextMutation{
		ExpectedRevision: revision(), PeriodKey: "2027-04-01", OpensAt: opensAt + 7*86400,
		Entry: "0.001", PerUserLimit: 1, PumpsBP: activities.PumpsBP{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PatchActivitiesConfig(ctx, adminID, mutation(http.MethodPatch, configRoute), activities.ActivitiesConfigPatch{
		ExpectedRevision: revision(), MasterEnabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	assertStations("next period enabled")
}
