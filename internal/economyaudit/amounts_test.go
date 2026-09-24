package economyaudit

import (
	"math/big"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestFinancialClassificationUsesExternalNetNotOperationLabel(t *testing.T) {
	p := func(kind string, n int64) posting { return posting{ledger.General, kind, big.NewInt(n)} }
	for _, test := range []struct {
		name                                         string
		entries                                      []posting
		issued, reclaimed, income, expense, transfer string
	}{
		{"mint", []posting{p("external", -5000), p("user", 5000)}, "5000", "0", "5000", "0", "0"},
		{"refund", []posting{p("platform", -5000), p("user", 5000)}, "0", "0", "5000", "0", "5000"},
		{"pool payout", []posting{p("pool", -5000), p("user", 5000)}, "0", "0", "5000", "0", "5000"},
		{"mixed game settlement", []posting{p("platform", -3000), p("platform", 3000), p("external", -5000), p("user", 5000)}, "5000", "0", "5000", "0", "3000"},
		{"retirement", []posting{p("user", -5000), p("external", 5000)}, "0", "5000", "0", "5000", "0"},
		{"debt forgiveness", []posting{p("user", 5000), p("external", -5000)}, "5000", "0", "5000", "0", "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			values, err := classifyPostings(test.entries)
			if err != nil {
				t.Fatal(err)
			}
			got := values[ledger.General].wire()
			if got.Issued != test.issued || got.Reclaimed != test.reclaimed || got.UserIncome != test.income || got.UserExpense != test.expense || got.InternalTransfer != test.transfer {
				t.Fatalf("metrics: %+v", got)
			}
		})
	}
}

func TestFinancialClassificationRejectsCrossCurrencyConservationAndFractionalActivity(t *testing.T) {
	for _, entries := range [][]posting{
		{{ledger.General, "external", big.NewInt(-1000)}, {ledger.SketchPaper, "user", big.NewInt(1000)}},
		{{ledger.SketchPaper, "external", big.NewInt(-1001)}, {ledger.SketchPaper, "user", big.NewInt(1001)}},
	} {
		if _, err := classifyPostings(entries); err == nil {
			t.Fatal("invalid asset posting accepted")
		}
	}
}

func TestAggregateTurnoverCanExceedOneAccountMagnitude(t *testing.T) {
	m := &measures{}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	n := &measures{count: 1}
	n.issued.Set(max)
	if err := m.add(n); err != nil {
		t.Fatal(err)
	}
	if err := m.add(n); err != nil {
		t.Fatal(err)
	}
	if m.issued.Cmp(new(big.Int).Mul(max, big.NewInt(2))) != 0 {
		t.Fatal("wide turnover truncated")
	}
}

func TestBucketsRespectHalfHourOffsetsAndEpoch(t *testing.T) {
	for _, test := range []struct {
		at         int64
		offset     int
		step, want int64
	}{{0, 840, 86400, -50400}, {0, 330, 3600, -1800}, {1800, 330, 3600, 1800}, {0, -720, 86400, -43200}, {86399, 0, 86400, 0}} {
		if got := bucketStart(test.at, test.offset, test.step); got != test.want {
			t.Fatalf("bucket %+v = %d", test, got)
		}
	}
}
