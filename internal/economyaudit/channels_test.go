package economyaudit

import (
	"context"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestSharedDuelKindsKeepSeparateChannelDrilldowns(t *testing.T) {
	f := newAuditFixture(t)
	for _, kind := range []ledger.Kind{ledger.KindDuelQueueReserve, ledger.KindDuelQueueRelease, ledger.KindDuelSessionStart, ledger.KindDuelTerminal} {
		for _, channel := range []string{"bidding", "likes", "unclassified", "admin"} {
			filter := auditFilter(ledger.General)
			filter.Kind = string(kind)
			filter.Channel = channel
			rows, err := f.database.QueryContext(context.Background(), `WITH o(source_id) AS (VALUES('bid_example'),('bidq_example'),('lik_example'),('likq_example'),('future_example')) SELECT source_id FROM o WHERE 1`+operationChannelCondition(filter, "o.source_id"))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for rows.Next() {
				var source string
				if err := rows.Scan(&source); err != nil {
					t.Fatal(err)
				}
				if ledger.ClassifyForAudit(kind, source).Channel != channel {
					t.Fatal("drilldown combined distinct games", kind, channel, source)
				}
				count++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			want := map[string]int{"bidding": 2, "likes": 2, "unclassified": 1, "admin": 0}[channel]
			if count != want {
				t.Fatalf("%s %s has %d rows, want %d", kind, channel, count, want)
			}
		}
	}
	filter := auditFilter(ledger.General)
	filter.Channel = "admin"
	if validateFilter(filter) == nil {
		t.Fatal("channel without operation kind accepted")
	}
	filter.Kind = string(ledger.KindAdminUserAdjustment)
	if operationChannelCondition(filter, "o.source_id") != "" {
		t.Fatal("known fixed channel rejected")
	}
	filter.Channel = "welfare"
	if operationChannelCondition(filter, "o.source_id") != " AND 0" {
		t.Fatal("mismatched fixed channel accepted")
	}
}
