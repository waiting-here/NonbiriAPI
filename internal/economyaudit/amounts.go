package economyaudit

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type measures struct {
	issued, reclaimed, income, expense, transfer big.Int
	count                                        int64
}

func (m *measures) add(n *measures) error {
	if n == nil || n.count < 0 || m.count > int64(^uint64(0)>>1)-n.count {
		return ErrInvariant
	}
	m.issued.Add(&m.issued, &n.issued)
	m.reclaimed.Add(&m.reclaimed, &n.reclaimed)
	m.income.Add(&m.income, &n.income)
	m.expense.Add(&m.expense, &n.expense)
	m.transfer.Add(&m.transfer, &n.transfer)
	m.count += n.count
	for _, amount := range []*big.Int{&m.issued, &m.reclaimed, &m.income, &m.expense, &m.transfer} {
		if amount.Sign() < 0 || len(amount.String()) > 64 {
			return ErrInvariant
		}
	}
	return nil
}

func (m *measures) wire() Metrics {
	return Metrics{m.issued.String(), m.reclaimed.String(), m.income.String(), m.expense.String(), m.transfer.String(), strconv.FormatInt(m.count, 10)}
}

func zeroMetrics() Metrics { return (&measures{}).wire() }

func decimal(value string) (*big.Int, error) {
	if len(value) == 0 || len(value) > 64 || len(value) > 1 && value[0] == '0' {
		return nil, ErrInvariant
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return nil, ErrInvariant
		}
	}
	n, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil, ErrInvariant
	}
	return n, nil
}

func decodeMeasures(values [5]string, count int64) (*measures, error) {
	if count < 0 {
		return nil, ErrInvariant
	}
	m := &measures{count: count}
	for i, dest := range []*big.Int{&m.issued, &m.reclaimed, &m.income, &m.expense, &m.transfer} {
		n, err := decimal(values[i])
		if err != nil {
			return nil, err
		}
		dest.Set(n)
	}
	return m, nil
}

func validAsset(a ledger.Asset) bool {
	for _, asset := range ledger.Assets() {
		if a == asset {
			return true
		}
	}
	return false
}

type posting struct {
	asset ledger.Asset
	kind  string
	delta *big.Int
}

func classifyPostings(entries []posting) (map[ledger.Asset]*measures, error) {
	type totals struct{ external, positive, negative, income, expense, conservation big.Int }
	assets := map[ledger.Asset]*totals{}
	for _, e := range entries {
		if !validAsset(e.asset) || e.delta == nil {
			return nil, ErrInvariant
		}
		if _, err := db.SM128FromBig(e.delta); err != nil {
			return nil, ErrInvariant
		}
		if e.asset.IsActivity() && new(big.Int).Mod(e.delta, big.NewInt(1000)).Sign() != 0 {
			return nil, ErrInvariant
		}
		t := assets[e.asset]
		if t == nil {
			t = &totals{}
			assets[e.asset] = t
		}
		t.conservation.Add(&t.conservation, e.delta)
		if e.kind == "external" {
			t.external.Add(&t.external, e.delta)
			continue
		}
		if e.kind != "user" && e.kind != "pool" && e.kind != "platform" {
			return nil, ErrInvariant
		}
		if e.delta.Sign() > 0 {
			t.positive.Add(&t.positive, e.delta)
		} else {
			t.negative.Sub(&t.negative, e.delta)
		}
		if e.kind == "user" {
			if e.delta.Sign() > 0 {
				t.income.Add(&t.income, e.delta)
			} else {
				t.expense.Sub(&t.expense, e.delta)
			}
		}
	}
	out := map[ledger.Asset]*measures{}
	for asset, t := range assets {
		if t.conservation.Sign() != 0 {
			return nil, ErrInvariant
		}
		m := &measures{count: 1}
		m.income.Set(&t.income)
		m.expense.Set(&t.expense)
		if t.external.Sign() < 0 {
			m.issued.Neg(&t.external)
		} else {
			m.reclaimed.Set(&t.external)
		}
		if t.positive.Cmp(&t.negative) < 0 {
			m.transfer.Set(&t.positive)
		} else {
			m.transfer.Set(&t.negative)
		}
		out[asset] = m
	}
	return out, nil
}

func bucketStart(at int64, offset int, seconds int64) int64 {
	shifted := at + int64(offset)*60
	q := shifted / seconds
	if shifted%seconds < 0 {
		q--
	}
	return q*seconds - int64(offset)*60
}

func frozenAccount(code string) bool {
	return code == "forward_reserve" || code == "charity_reserve" || code == "game_fishing_reserve" || code == "image_activity_reserve" ||
		strings.HasPrefix(code, "rps-queue:") || strings.HasPrefix(code, "rps-session:") || strings.HasPrefix(code, "duel-queue:") ||
		strings.HasPrefix(code, "duel-session:") || strings.HasPrefix(code, "blackjack-payment:")
}
