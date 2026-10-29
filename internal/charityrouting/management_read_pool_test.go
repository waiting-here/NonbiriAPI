package charityrouting

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestManagementPureReadsAvoidWriterAndUseCurrentAuthority(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	six := int64(6)
	steward := e.seedUser(t, false, &six)
	model := e.createModel(t, 'A')
	modelID, _ := parsePositiveID(model.ID)
	input := templateInput()
	made, err := e.service.mutatePriceTemplate(t.Context(), 0, "", routingMutation(t, 'T', http.MethodPost, routeAdminPriceTemplates, nil, input), &input)
	if err != nil {
		t.Fatal(err)
	}
	templateID, _ := parsePositiveID(made.Value.ID)
	reads := []struct {
		name string
		read func(context.Context) error
	}{
		{"model detail", func(ctx context.Context) error {
			v, err := e.service.GetSteward(ctx, steward, modelID)
			if err == nil && v.ID != model.ID {
				t.Error("wrong detail")
			}
			return err
		}},
		{"cursor models", func(ctx context.Context) error {
			v, _, err := e.service.ListSteward(ctx, steward, "", nil, 0, 20)
			if err == nil && len(v) != 1 {
				t.Error("wrong list")
			}
			return err
		}},
		{"price templates", func(ctx context.Context) error {
			v, err := e.service.priceTemplates(ctx, roleSteward, steward, "", "enabled", pagination.Default())
			if err == nil && len(v.Data) != 1 {
				t.Error("wrong templates")
			}
			return err
		}},
		{"price template", func(ctx context.Context) error {
			v, err := e.service.priceTemplate(ctx, roleSteward, steward, templateID)
			if err == nil && v.ID != made.Value.ID {
				t.Error("wrong template")
			}
			return err
		}},
	}
	held, err := e.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var before, after int64
	if err := held.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for _, read := range reads {
		if err := read.read(ctx); err != nil {
			t.Fatalf("%s waited for writer: %v", read.name, err)
		}
		if e.store.ReadDB().Stats().InUse != 0 {
			t.Fatalf("%s retained reader after return", read.name)
		}
	}
	if _, err := e.service.priceTemplates(ctx, roleAdmin, 0, "", "all", pagination.Default()); err != nil {
		t.Fatal("admin template list", err)
	}
	if _, err := e.service.priceTemplate(ctx, roleAdmin, 0, templateID); err != nil {
		t.Fatal("admin template detail", err)
	}
	if err := held.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before || e.state.dueCalls.Load() != 0 {
		t.Fatal("pure read performed a mutation or materialized expiry", before, after)
	}
	if _, err := held.ExecContext(ctx, "UPDATE users SET level=1 WHERE id=?", steward); err != nil {
		t.Fatal(err)
	}
	for _, read := range reads {
		if err := read.read(ctx); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s reused stale authority: %v", read.name, err)
		}
	}
	if _, err := held.ExecContext(ctx, "UPDATE users SET level=6 WHERE id=?", steward); err != nil {
		t.Fatal(err)
	}
	if _, err := held.ExecContext(ctx, "UPDATE charity_price_templates SET enabled=0 WHERE id=?", templateID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.priceTemplate(ctx, roleSteward, steward, templateID); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled template exposed", err)
	}
	list, err := e.service.priceTemplates(ctx, roleSteward, steward, "", "enabled", pagination.Default())
	if err != nil || len(list.Data) != 0 {
		t.Fatal("disabled template listed", list, err)
	}
	if e.store.ReadDB().Stats().InUse != 0 {
		t.Fatal("denied read retained reader")
	}
}
