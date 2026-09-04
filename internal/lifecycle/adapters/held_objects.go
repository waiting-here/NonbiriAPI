package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

type MaintenanceHeldObjectOwner interface {
	InspectForCreate(context.Context, *sql.Tx, string, int64) (maintenance.HeldEventState, error)
	ConsumeMarker(context.Context, *sql.Tx, string) error
	ReadHeld(context.Context, *sql.Tx, string, int64) (bool, error)
}

type DonationHeldObjectOwner interface {
	InspectForCreate(context.Context, *sql.Tx, string, int64) (donation.HeldDonationState, error)
	ConsumeMarker(context.Context, *sql.Tx, string) error
	ReadHeld(context.Context, *sql.Tx, string, int64) (bool, error)
}

type RequestLogHeldObjectOwner interface {
	InspectForCreate(context.Context, *sql.Tx, string, int64) (logapi.HeldRequestLogState, error)
	ConsumeMarker(context.Context, *sql.Tx, string) error
	ReadHeld(context.Context, *sql.Tx, string, int64) (bool, error)
}

type MaintenanceHeldObject struct{ owner MaintenanceHeldObjectOwner }
type DonationHeldObject struct{ owner DonationHeldObjectOwner }
type RequestLogHeldObject struct{ owner RequestLogHeldObjectOwner }

func NewMaintenanceHeldObject(owner MaintenanceHeldObjectOwner) *MaintenanceHeldObject {
	return &MaintenanceHeldObject{owner: owner}
}

func NewDonationHeldObject(owner DonationHeldObjectOwner) *DonationHeldObject {
	return &DonationHeldObject{owner: owner}
}

func NewRequestLogHeldObject(owner RequestLogHeldObjectOwner) *RequestLogHeldObject {
	return &RequestLogHeldObject{owner: owner}
}

func (adapter *MaintenanceHeldObject) InspectForCreate(
	ctx context.Context, tx *sql.Tx, ref string, now int64,
) (lifecycle.HeldObjectState, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.HeldObjectState{}, lifecycle.ErrUnavailable
	}
	state, err := adapter.owner.InspectForCreate(ctx, tx, ref, now)
	if err != nil {
		return lifecycle.HeldObjectState{}, translateHeldObjectError(err)
	}
	return lifecycle.HeldObjectState{
		Exists: state.Exists, OrdinaryDeadline: state.OrdinaryDeadline,
		LegalHoldConsumed: state.LegalHoldConsumed,
	}, nil
}

func (adapter *MaintenanceHeldObject) ConsumeMarker(ctx context.Context, tx *sql.Tx, ref string) error {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.ErrUnavailable
	}
	return translateHeldObjectError(adapter.owner.ConsumeMarker(ctx, tx, ref))
}

func (adapter *MaintenanceHeldObject) ReadHeld(ctx context.Context, tx *sql.Tx, ref string, now int64) (bool, error) {
	if adapter == nil || adapter.owner == nil {
		return false, lifecycle.ErrUnavailable
	}
	exists, err := adapter.owner.ReadHeld(ctx, tx, ref, now)
	return exists, translateHeldObjectError(err)
}

func (adapter *DonationHeldObject) InspectForCreate(
	ctx context.Context, tx *sql.Tx, ref string, now int64,
) (lifecycle.HeldObjectState, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.HeldObjectState{}, lifecycle.ErrUnavailable
	}
	state, err := adapter.owner.InspectForCreate(ctx, tx, ref, now)
	if err != nil {
		return lifecycle.HeldObjectState{}, translateHeldObjectError(err)
	}
	return lifecycle.HeldObjectState{
		Exists: state.Exists, OrdinaryDeadline: state.OrdinaryDeadline,
		LegalHoldConsumed: state.LegalHoldConsumed,
	}, nil
}

func (adapter *DonationHeldObject) ConsumeMarker(ctx context.Context, tx *sql.Tx, ref string) error {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.ErrUnavailable
	}
	return translateHeldObjectError(adapter.owner.ConsumeMarker(ctx, tx, ref))
}

func (adapter *DonationHeldObject) ReadHeld(ctx context.Context, tx *sql.Tx, ref string, now int64) (bool, error) {
	if adapter == nil || adapter.owner == nil {
		return false, lifecycle.ErrUnavailable
	}
	exists, err := adapter.owner.ReadHeld(ctx, tx, ref, now)
	return exists, translateHeldObjectError(err)
}

func (adapter *RequestLogHeldObject) InspectForCreate(
	ctx context.Context, tx *sql.Tx, ref string, now int64,
) (lifecycle.HeldObjectState, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.HeldObjectState{}, lifecycle.ErrUnavailable
	}
	state, err := adapter.owner.InspectForCreate(ctx, tx, ref, now)
	if err != nil {
		return lifecycle.HeldObjectState{}, translateHeldObjectError(err)
	}
	return lifecycle.HeldObjectState{
		Exists: state.Exists, OrdinaryDeadline: state.OrdinaryDeadline,
		LegalHoldConsumed: state.LegalHoldConsumed,
	}, nil
}

func (adapter *RequestLogHeldObject) ConsumeMarker(ctx context.Context, tx *sql.Tx, ref string) error {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.ErrUnavailable
	}
	return translateHeldObjectError(adapter.owner.ConsumeMarker(ctx, tx, ref))
}

func (adapter *RequestLogHeldObject) ReadHeld(ctx context.Context, tx *sql.Tx, ref string, now int64) (bool, error) {
	if adapter == nil || adapter.owner == nil {
		return false, lifecycle.ErrUnavailable
	}
	exists, err := adapter.owner.ReadHeld(ctx, tx, ref, now)
	return exists, translateHeldObjectError(err)
}

func translateHeldObjectError(err error) error {
	if err == nil {
		return nil
	}
	var target error
	switch {
	case errors.Is(err, maintenance.ErrInvalidMutation), errors.Is(err, donation.ErrInvalidRequest),
		errors.Is(err, logapi.ErrInvalid):
		target = lifecycle.ErrInvalid
	case errors.Is(err, maintenance.ErrConflict), errors.Is(err, donation.ErrConflict),
		errors.Is(err, logapi.ErrConflict):
		target = lifecycle.ErrConflict
	case errors.Is(err, maintenance.ErrInvariant), errors.Is(err, donation.ErrInvariant),
		errors.Is(err, logapi.ErrInvariant):
		target = lifecycle.ErrInvariant
	case errors.Is(err, donation.ErrNotFound), errors.Is(err, logapi.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		target = lifecycle.ErrNotFound
	case errors.Is(err, donation.ErrUnavailable), errors.Is(err, logapi.ErrUnavailable),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		target = lifecycle.ErrUnavailable
	default:
		target = lifecycle.ErrUnavailable
	}
	return fmt.Errorf("%w: %v", target, err)
}

var (
	_ MaintenanceHeldObjectOwner = (*maintenance.Service)(nil)
	_ DonationHeldObjectOwner    = (*donation.Service)(nil)
	_ RequestLogHeldObjectOwner  = (*logapi.Repository)(nil)

	_ lifecycle.HeldObjectAdapter = (*MaintenanceHeldObject)(nil)
	_ lifecycle.HeldObjectAdapter = (*DonationHeldObject)(nil)
	_ lifecycle.HeldObjectAdapter = (*RequestLogHeldObject)(nil)
)
