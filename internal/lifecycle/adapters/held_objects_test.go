package adapters

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"

	_ "modernc.org/sqlite"
)

type heldObjectProbe struct {
	expectedTx  *sql.Tx
	expectedNow int64
	inspectRef  string
	consumeRef  string
	readRef     string
}

func (probe *heldObjectProbe) inspect(tx *sql.Tx, ref string, now int64) error {
	if tx != probe.expectedTx || now != probe.expectedNow {
		return errors.New("held adapter did not preserve caller transaction and decision time")
	}
	probe.inspectRef = ref
	return nil
}

func (probe *heldObjectProbe) consume(tx *sql.Tx, ref string) error {
	if tx != probe.expectedTx {
		return errors.New("held adapter did not preserve marker transaction")
	}
	probe.consumeRef = ref
	return nil
}

func (probe *heldObjectProbe) read(tx *sql.Tx, ref string, now int64) error {
	if tx != probe.expectedTx || now != probe.expectedNow {
		return errors.New("held adapter did not preserve read transaction and decision time")
	}
	probe.readRef = ref
	return nil
}

type fakeMaintenanceHeldObjectOwner struct {
	probe      *heldObjectProbe
	state      maintenance.HeldEventState
	inspectErr error
	consumeErr error
	readExists bool
	readErr    error
}

func (owner *fakeMaintenanceHeldObjectOwner) InspectForCreate(
	_ context.Context, tx *sql.Tx, ref string, now int64,
) (maintenance.HeldEventState, error) {
	if err := owner.probe.inspect(tx, ref, now); err != nil {
		return maintenance.HeldEventState{}, err
	}
	return owner.state, owner.inspectErr
}

func (owner *fakeMaintenanceHeldObjectOwner) ConsumeMarker(
	_ context.Context, tx *sql.Tx, ref string,
) error {
	if err := owner.probe.consume(tx, ref); err != nil {
		return err
	}
	return owner.consumeErr
}

func (owner *fakeMaintenanceHeldObjectOwner) ReadHeld(
	_ context.Context, tx *sql.Tx, ref string, now int64,
) (bool, error) {
	if err := owner.probe.read(tx, ref, now); err != nil {
		return false, err
	}
	return owner.readExists, owner.readErr
}

type fakeDonationHeldObjectOwner struct {
	probe      *heldObjectProbe
	state      donation.HeldDonationState
	inspectErr error
	consumeErr error
	readExists bool
	readErr    error
}

func (owner *fakeDonationHeldObjectOwner) InspectForCreate(
	_ context.Context, tx *sql.Tx, ref string, now int64,
) (donation.HeldDonationState, error) {
	if err := owner.probe.inspect(tx, ref, now); err != nil {
		return donation.HeldDonationState{}, err
	}
	return owner.state, owner.inspectErr
}

func (owner *fakeDonationHeldObjectOwner) ConsumeMarker(
	_ context.Context, tx *sql.Tx, ref string,
) error {
	if err := owner.probe.consume(tx, ref); err != nil {
		return err
	}
	return owner.consumeErr
}

func (owner *fakeDonationHeldObjectOwner) ReadHeld(
	_ context.Context, tx *sql.Tx, ref string, now int64,
) (bool, error) {
	if err := owner.probe.read(tx, ref, now); err != nil {
		return false, err
	}
	return owner.readExists, owner.readErr
}

type fakeRequestLogHeldObjectOwner struct {
	probe      *heldObjectProbe
	state      logapi.HeldRequestLogState
	inspectErr error
	consumeErr error
	readExists bool
	readErr    error
}

func (owner *fakeRequestLogHeldObjectOwner) InspectForCreate(
	_ context.Context, tx *sql.Tx, ref string, now int64,
) (logapi.HeldRequestLogState, error) {
	if err := owner.probe.inspect(tx, ref, now); err != nil {
		return logapi.HeldRequestLogState{}, err
	}
	return owner.state, owner.inspectErr
}

func (owner *fakeRequestLogHeldObjectOwner) ConsumeMarker(
	_ context.Context, tx *sql.Tx, ref string,
) error {
	if err := owner.probe.consume(tx, ref); err != nil {
		return err
	}
	return owner.consumeErr
}

func (owner *fakeRequestLogHeldObjectOwner) ReadHeld(
	_ context.Context, tx *sql.Tx, ref string, now int64,
) (bool, error) {
	if err := owner.probe.read(tx, ref, now); err != nil {
		return false, err
	}
	return owner.readExists, owner.readErr
}

func newHeldObjectAdapterTransaction(t *testing.T) *sql.Tx {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestHeldObjectAdaptersPreserveTypedOwnerBoundary(t *testing.T) {
	tx := newHeldObjectAdapterTransaction(t)
	decisionNow := int64(1_700_000_123)
	tests := []struct {
		name    string
		ref     string
		probe   *heldObjectProbe
		adapter lifecycle.HeldObjectAdapter
		want    lifecycle.HeldObjectState
	}{
		{
			name: "maintenance", ref: "op_event", probe: &heldObjectProbe{expectedTx: tx, expectedNow: decisionNow},
			want: lifecycle.HeldObjectState{Exists: true, OrdinaryDeadline: 11, LegalHoldConsumed: true},
		},
		{
			name: "donation", ref: "12", probe: &heldObjectProbe{expectedTx: tx, expectedNow: decisionNow},
			want: lifecycle.HeldObjectState{Exists: true, OrdinaryDeadline: 22},
		},
		{
			name: "request_log", ref: "13", probe: &heldObjectProbe{expectedTx: tx, expectedNow: decisionNow},
			want: lifecycle.HeldObjectState{Exists: true, OrdinaryDeadline: 33, LegalHoldConsumed: true},
		},
	}
	tests[0].adapter = NewMaintenanceHeldObject(&fakeMaintenanceHeldObjectOwner{
		probe: tests[0].probe, state: maintenance.HeldEventState{
			Exists: true, OrdinaryDeadline: 11, LegalHoldConsumed: true,
		}, readExists: true,
	})
	tests[1].adapter = NewDonationHeldObject(&fakeDonationHeldObjectOwner{
		probe: tests[1].probe, state: donation.HeldDonationState{Exists: true, OrdinaryDeadline: 22}, readExists: true,
	})
	tests[2].adapter = NewRequestLogHeldObject(&fakeRequestLogHeldObjectOwner{
		probe: tests[2].probe, state: logapi.HeldRequestLogState{
			Exists: true, OrdinaryDeadline: 33, LegalHoldConsumed: true,
		}, readExists: true,
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := test.adapter.InspectForCreate(context.Background(), tx, test.ref, decisionNow)
			if err != nil {
				t.Fatal(err)
			}
			if state != test.want {
				t.Fatalf("state = %+v, want %+v", state, test.want)
			}
			if err := test.adapter.ConsumeMarker(context.Background(), tx, test.ref); err != nil {
				t.Fatal(err)
			}
			exists, err := test.adapter.ReadHeld(context.Background(), tx, test.ref, decisionNow)
			if err != nil || !exists {
				t.Fatalf("read held = %v, %v", exists, err)
			}
			if test.probe.inspectRef != test.ref || test.probe.consumeRef != test.ref || test.probe.readRef != test.ref {
				t.Fatalf("owner refs = inspect:%q consume:%q read:%q",
					test.probe.inspectRef, test.probe.consumeRef, test.probe.readRef)
			}
		})
	}
}

func TestHeldObjectAdapterErrorTranslationAndMissingOwners(t *testing.T) {
	tx := newHeldObjectAdapterTransaction(t)
	decisionNow := int64(10)
	probe := &heldObjectProbe{expectedTx: tx, expectedNow: decisionNow}
	owner := &fakeDonationHeldObjectOwner{probe: probe, inspectErr: donation.ErrInvalidRequest}
	adapter := NewDonationHeldObject(owner)
	if _, err := adapter.InspectForCreate(context.Background(), tx, "1", decisionNow); !errors.Is(err, lifecycle.ErrInvalid) {
		t.Fatalf("invalid translation = %v", err)
	}
	owner.inspectErr = nil
	owner.consumeErr = donation.ErrConflict
	if err := adapter.ConsumeMarker(context.Background(), tx, "1"); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatalf("conflict translation = %v", err)
	}
	owner.consumeErr = nil
	owner.readErr = donation.ErrInvariant
	if _, err := adapter.ReadHeld(context.Background(), tx, "1", decisionNow); !errors.Is(err, lifecycle.ErrInvariant) {
		t.Fatalf("invariant translation = %v", err)
	}

	missing := []lifecycle.HeldObjectAdapter{
		NewMaintenanceHeldObject(nil), NewDonationHeldObject(nil), NewRequestLogHeldObject(nil),
	}
	for _, subject := range missing {
		if _, err := subject.InspectForCreate(context.Background(), tx, "1", decisionNow); !errors.Is(err, lifecycle.ErrUnavailable) {
			t.Fatalf("missing owner error = %v", err)
		}
	}
}
