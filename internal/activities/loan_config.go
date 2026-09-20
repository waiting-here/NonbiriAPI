package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type LoanConfig struct {
	LoanEnabled bool      `json:"loan_enabled"`
	LoanTiers   [3]string `json:"loan_tiers"`
	LoanA       string    `json:"loan_a"`
	LoanB       string    `json:"loan_b"`
}

type LoanView struct {
	Enabled   bool     `json:"enabled"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason"`
	Tiers     []string `json:"tiers"`
}

type loanConfig struct {
	enabled bool
	tiers   [3]string
	a, b    int64
}

func (c loanConfig) terms(tier int) (db.LoanTerms, error) {
	if tier < 1 || tier > 3 {
		return db.LoanTerms{}, ErrInvalidRequest
	}
	terms, err := db.CalculateLoanTerms(c.tiers[tier-1], strconv.FormatInt(c.a, 10), strconv.FormatInt(c.b, 10))
	if err != nil {
		return db.LoanTerms{}, ErrInvalidRequest
	}
	return terms, nil
}

func (c loanConfig) valid() bool {
	previous := int64(0)
	for tier := 1; tier <= 3; tier++ {
		t, err := c.terms(tier)
		if err != nil || t.Principal <= previous {
			return false
		}
		previous = t.Principal
	}
	return true
}

func (c loanConfig) wire() LoanConfig {
	return LoanConfig{c.enabled, c.tiers, formatMilliPointsInt64(c.a), formatMilliPointsInt64(c.b)}
}

func readLoanConfigTx(ctx context.Context, tx *sql.Tx) (loanConfig, error) {
	var c loanConfig
	var enabled, tiers, a, b string
	for key, value := range map[string]*string{"enabled": &enabled, "tiers": &tiers, "a_milli": &a, "b_milli": &b} {
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, "activity_loan_"+key).Scan(value); err != nil {
			return c, classifyDatabaseError("read loan configuration", err)
		}
	}
	var ok bool
	if c.enabled, ok = parseConfigBool(enabled); !ok {
		return c, ErrInvariant
	}
	var values []string
	if json.Unmarshal([]byte(tiers), &values) != nil || len(values) != 3 {
		return c, ErrInvariant
	}
	copy(c.tiers[:], values)
	if c.a, ok = parseStoredConfigMilli(a); !ok {
		return c, ErrInvariant
	}
	if c.b, ok = parseStoredConfigMilli(b); !ok || !c.valid() {
		return c, ErrInvariant
	}
	return c, nil
}

func (c *loanConfig) patch(p ActivitiesConfigPatch) error {
	if p.LoanEnabled != nil {
		c.enabled = *p.LoanEnabled
	}
	if p.LoanTiers != nil {
		if len(*p.LoanTiers) != 3 {
			return ErrInvalidRequest
		}
		copy(c.tiers[:], *p.LoanTiers)
	}
	for _, item := range []struct {
		input  *string
		target *int64
	}{{p.LoanA, &c.a}, {p.LoanB, &c.b}} {
		if item.input != nil {
			value, ok := parsePointsMilli(*item.input)
			if !ok {
				return ErrInvalidRequest
			}
			*item.target = value
		}
	}
	if !c.valid() {
		return ErrInvalidRequest
	}
	return nil
}

func (p ActivitiesConfigPatch) hasLoan() bool {
	return p.LoanEnabled != nil || p.LoanTiers != nil || p.LoanA != nil || p.LoanB != nil
}

func (c loanConfig) save(ctx context.Context, tx *sql.Tx, now int64) error {
	tiers, err := json.Marshal(c.tiers)
	if err != nil {
		return ErrInvariant
	}
	for key, value := range map[string]string{"enabled": boolConfig(c.enabled), "tiers": string(tiers), "a_milli": strconv.FormatInt(c.a, 10), "b_milli": strconv.FormatInt(c.b, 10)} {
		if _, err := tx.ExecContext(ctx, `UPDATE site_config SET value=?,updated_at=? WHERE key=?`, value, now, "activity_loan_"+key); err != nil {
			return classifyDatabaseError("save loan configuration", err)
		}
	}
	return nil
}
