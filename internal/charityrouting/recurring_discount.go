package charityrouting

import (
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/charitydiscount"
)

func normalizeRecurring(input *charitydiscount.Config) (charitydiscount.Config, string, error) {
	value := charitydiscount.Default()
	if input != nil {
		value = *input
	}
	value, err := charitydiscount.Normalize(value)
	if err != nil {
		return charitydiscount.Config{}, "", ErrInvalidRequest
	}
	encoded, err := charitydiscount.Encode(value)
	if err != nil {
		return charitydiscount.Config{}, "", ErrInvalidRequest
	}
	return value, encoded, nil
}

func effectiveDiscount(enabled bool, percent int, start, end *int64, recurring charitydiscount.Config, at int64) (charitydiscount.Effective, error) {
	value, err := charitydiscount.Evaluate(charitydiscount.Fixed{Enabled: enabled, Percent: percent, StartAt: start, EndAt: end}, recurring, at)
	if err != nil {
		return charitydiscount.Effective{}, ErrInvariant
	}
	return value, nil
}

func projectAdminDiscount(value *AdminCharityModel, at int64) error {
	effective, err := effectiveDiscount(value.Discount.Enabled, value.Discount.Percent, value.Discount.StartAt, value.Discount.EndAt, value.RecurringDiscount, at)
	if err != nil {
		return err
	}
	value.EffectiveDiscount, value.ServerNow = effective, at
	return nil
}

func projectCapabilityDiscount(value *CapabilityModel, encoded string, at int64) error {
	recurring, err := charitydiscount.Decode(encoded)
	if err != nil {
		return ErrInvariant
	}
	effective, err := effectiveDiscount(value.Discount.Enabled, value.Discount.Percent, value.Discount.StartAt, value.Discount.EndAt, recurring, at)
	if err != nil {
		return err
	}
	value.RecurringDiscount, value.EffectiveDiscount = recurring, effective
	return nil
}

func activeDiscountAt(percent, enabled int, start, end sql.NullInt64, encoded string, at int64) (int, error) {
	recurring, err := charitydiscount.Decode(encoded)
	if err != nil {
		return 0, ErrInvariant
	}
	value, err := charitydiscount.PercentAt(charitydiscount.Fixed{Enabled: enabled == 1, Percent: percent, StartAt: nullIntPointer(start), EndAt: nullIntPointer(end)}, recurring, at)
	if err != nil {
		return 0, ErrInvariant
	}
	return value, nil
}
