package agentrequest

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestUsageHoursPreserveExactRollingEdgesAndActors(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(db.AgentRequestsSQL); err != nil {
		t.Fatal(err)
	}
	const hour = int64(3600000)
	times := []int64{48*hour - 1, 48 * hour, 48*hour + 1, 49*hour - 1, 49 * hour, 60 * hour, 71*hour + 1, 72 * hour, 72*hour + 1, 73*hour - 1}
	for i, at := range times {
		if _, err := database.Exec(`INSERT INTO agent_request_usage VALUES(?,?,'admin_chat','model',?,'admin','saved',?,2,1,3,4,5,?)`, i+1, fmt.Sprint(i+1), i%2, at, i%3 == 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO agent_request_usage_totals
SELECT source,kind,actor_id,actor_role,'current label',count(*),sum(input_tokens),sum(cached_input_tokens),sum(cache_write_input_tokens),sum(output_tokens),sum(reasoning_tokens),sum(unknown)
FROM agent_request_usage GROUP BY source,kind,actor_id;
INSERT INTO agent_request_usage_hours
SELECT recorded_at/3600000*3600000,source,kind,actor_id,actor_role,actor_label,count(*),sum(input_tokens),sum(cached_input_tokens),sum(cache_write_input_tokens),sum(output_tokens),sum(reasoning_tokens),sum(unknown)
FROM agent_request_usage GROUP BY recorded_at/3600000,source,kind,actor_id;`); err != nil {
		t.Fatal(err)
	}
	for _, now := range []int64{72 * hour, 72*hour + 12345, 73*hour - 1} {
		runtime := &Runtime{readDB: database, now: func() time.Time { return time.UnixMilli(now) }}
		result, err := runtime.Usage(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var count [2]int64
		var unknown [2]bool
		for i, at := range times {
			if at > now-24*hour && at <= now {
				count[i%2]++
				unknown[i%2] = unknown[i%2] || i%3 == 0
			}
		}
		if len(result.Last24Hours) != 2 {
			t.Fatal(result)
		}
		for _, item := range result.Last24Hours {
			n := count[item.ActorID]
			if item.Requests != n || item.Usage.InputTokens != 2*n || item.Usage.CachedInputTokens != n || item.Usage.CacheWriteInputTokens != 3*n || item.Usage.OutputTokens != 4*n || item.Usage.ReasoningTokens != 5*n || item.Usage.Unknown != unknown[item.ActorID] || item.ActorLabel != "current label" {
				t.Fatalf("window=%d actor=%d: %+v, want count=%d", now, item.ActorID, item, n)
			}
		}
	}
}
