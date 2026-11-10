package riskaudit

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const riskEdgesExcludedFrom = ` FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id
WHERE s.occurred_at>=? AND s.occurred_at<? AND s.source_id<=?
AND s.kind IN ('self','charity','unclassified') AND (?='total' OR s.kind=?)
AND (l.origin_discord_id IS NULL OR l.origin_user_id IS NULL OR s.ip_quality NOT IN ('direct_peer','trusted_forwarded'))`

func seedRiskEdgesSources(t *testing.T, f *auditFixture, count int, user int64) {
	t.Helper()
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
SELECT printf('req_%021dA',x),?,'openai_chat_completions','model','terminal',1,'success',200,'none','user',zeroblob(16),?+x%3600,?+x%3600+1 FROM n`, count, user, f.now-7200, f.now-7200)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(t.Context(), `INSERT INTO request_logs(logical_request_id,user_id,model,route_kind,caller_result_class,caller_status,status_code,duration_ms,attempt_count,started_at,completed_at)
SELECT id,user_id,model_snapshot,route_kind,caller_result_class,caller_status,200,1000,0,created_at,terminal_at FROM logical_requests`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(t.Context(), `INSERT INTO request_source_facts(request_log_id,user_id,kind,effective_ip,ip_quality,source_json,occurred_at)
SELECT id,user_id,CASE id%3 WHEN 0 THEN 'charity' WHEN 1 THEN 'self' ELSE 'unclassified' END,'192.0.2.1',
CASE WHEN id%10=0 AND id%3<>0 THEN 'peer_fallback' ELSE 'direct_peer' END,
json_object('effective_ip','192.0.2.1','ip_quality',CASE WHEN id%10=0 AND id%3<>0 THEN 'peer_fallback' ELSE 'direct_peer' END,'user_agent','Example/1'),started_at FROM request_logs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func riskEdgesPlan(t *testing.T, database *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "; ")
}

type riskEdgesWriterAuth struct {
	FinalAuthorizer
	ready func() error
}

func (a riskEdgesWriterAuth) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, user int64) error {
	if err := a.FinalAuthorizer.AuthorizeAdmin(ctx, tx, user); err != nil {
		return err
	}
	return a.ready()
}

func measureRiskEdgesWriterWait(t *testing.T, f *auditFixture, input ScanInput, user int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	result := make(chan error, 1)
	var wait time.Duration
	before := f.store.DB().Stats()
	f.repository.finalAuth = riskEdgesWriterAuth{FinalAuthorizer: f.authority, ready: func() error {
		close(entered)
		// Synchronize a real pool waiter after the authorized scan has acquired
		// the writer. No SQL delay is added to the selection itself.
		for f.store.DB().Stats().WaitCount == before.WaitCount {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
		return nil
	}}
	defer func() { f.repository.finalAuth = f.authority }()
	go func() {
		select {
		case <-entered:
		case <-ctx.Done():
			result <- ctx.Err()
			return
		}
		start := time.Now()
		tx, err := f.store.DB().BeginTx(ctx, nil)
		wait = time.Since(start)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE users SET updated_at=updated_at WHERE id=?`, user)
			if err == nil {
				err = tx.Commit()
			} else {
				tx.Rollback()
			}
		}
		result <- err
	}()
	start := time.Now()
	scan, err := f.repository.CreateScan(ctx, Actor{Admin: true, UserID: f.admin}, input)
	wall := time.Since(start)
	writerErr := <-result
	f.repository.finalAuth = f.authority
	t.Logf("CreateScan concurrent writer: wall=%s writer_acquire=%s pool_wait=%s candidates=%d error=%v writer_error=%v", wall, wait, f.store.DB().Stats().WaitDuration-before.WaitDuration, scan.Candidates, err, writerErr)
	if err != nil || writerErr != nil {
		t.Fatal(err, writerErr)
	}
	if _, err := f.repository.CancelScan(t.Context(), Actor{Admin: true, UserID: f.admin}, scan.ID); err != nil {
		t.Fatal(err)
	}
}

func TestQueryRiskEdgesCreateScanScale(t *testing.T) {
	if os.Getenv("NONBIRI_QUERY_RISK_EDGES") != "1" {
		t.Skip("opt-in risk-edge query workload")
	}
	for _, count := range []int{10000, 100000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newAuditFixture(t)
			user := f.user(1)
			scanRuleFixture(t, f)
			start := time.Now()
			seedRiskEdgesSources(t, f, count, user)
			t.Logf("setup facts=%d elapsed=%s", count, time.Since(start))
			t.Logf("candidate shared_ips plan=%s", riskEdgesPlan(t, f.store.ReadDB(), `SELECT count(*) FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE s.occurred_at>=? AND s.occurred_at<? AND s.source_id<=? AND l.origin_discord_id IS NOT NULL AND l.origin_user_id IS NOT NULL AND s.kind IN ('self','charity','unclassified') AND (?='total' OR s.kind=?) AND s.ip_quality IN ('direct_peer','trusted_forwarded')`, f.now-86400, f.now, count, "total", "total"))
			if count == 10000 {
				for name, query := range map[string]string{
					"grants":           `SELECT g.id FROM endpoint_key_grant_revisions g JOIN endpoint_keys k ON k.id=g.endpoint_key_id JOIN credential_versions v ON v.id=g.credential_version_id WHERE g.owner_user_id=? ORDER BY g.endpoint_key_id,g.grant_revision LIMIT ?`,
					"source_revisions": `SELECT sr.id FROM discovery_source_revisions sr JOIN credential_versions v ON v.id=sr.credential_version_id WHERE sr.owner_user_id=? ORDER BY sr.source_id,sr.source_revision LIMIT ?`,
					"native_evidence":  `SELECT c.id FROM model_catalog_entries c LEFT JOIN endpoint_keys k ON k.id=c.endpoint_key_id LEFT JOIN endpoints e ON e.id=k.endpoint_id LEFT JOIN discovery_runs r ON r.id=c.discovery_run_id LEFT JOIN discovery_source_revisions sr ON sr.id=r.source_revision_id WHERE COALESCE(e.user_id,sr.owner_user_id)=? ORDER BY c.id LIMIT ?`,
				} {
					// These empty-table plans assess owner selection only. They are
					// not account-export scale or lifecycle transaction evidence.
					t.Logf("export selector=%s plan=%s", name, riskEdgesPlan(t, f.store.ReadDB(), query, user, 10001))
				}
			}
			actor := Actor{Admin: true, UserID: f.admin}
			excluded := count/10 - count/30
			for _, kind := range []string{"client_hits", "users", "shared_ips", "user_ips", "targeted_user_ips"} {
				for sample := range 2 {
					input := ScanInput{RequestToken: fmt.Sprintf("risk_edges_%s_%d", kind, sample), LookbackHours: 24, Kind: "total", ScanKind: kind}
					if kind == "targeted_user_ips" {
						input.ScanKind, input.UserID = "user_ips", ScanUserID(user)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					start := time.Now()
					scan, err := f.repository.CreateScan(ctx, actor, input)
					cancel()
					t.Logf("CreateScan kind=%s sample=%d elapsed=%s candidates=%d coverage=%s error=%v", kind, sample, time.Since(start), scan.Candidates, scan.Coverage, err)
					want := count
					if kind != "client_hits" && kind != "users" {
						want -= excluded
					}
					if err != nil || scan.Candidates != int64(want) || scan.upperSource != int64(count) {
						t.Fatal("candidate count or frozen watermark", scan, err)
					}
					replay, err := f.repository.CreateScan(t.Context(), actor, input)
					if err != nil || replay.ID != scan.ID || replay.SourceWatermark != scan.SourceWatermark || replay.checkpoint != scan.checkpoint || replay.FilterRevision != scan.FilterRevision {
						t.Fatal("creation replay changed frozen snapshot", replay, err)
					}
					if _, err := f.repository.CancelScan(t.Context(), actor, scan.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, kind := range []string{"total", "charity"} {
				args := []any{f.now - 86400, f.now, count, kind, kind}
				for _, mode := range []string{"count", "exists"} {
					query := `SELECT count(*)` + riskEdgesExcludedFrom
					if mode == "exists" {
						query = `SELECT EXISTS(SELECT 1` + riskEdgesExcludedFrom + `)`
					}
					for sample := range 2 {
						ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
						start := time.Now()
						var found int
						err := f.store.ReadDB().QueryRowContext(ctx, query, args...).Scan(&found)
						cancel()
						t.Logf("exclusion kind=%s mode=%s sample=%d elapsed=%s value=%d err=%v", kind, mode, sample, time.Since(start), found, err)
						if err != nil || (found > 0) != (kind == "total") {
							t.Fatal("exclusion boolean changed", found, err)
						}
					}
					t.Logf("exclusion kind=%s mode=%s plan=%s", kind, mode, riskEdgesPlan(t, f.store.ReadDB(), query, args...))
				}
			}
			measureRiskEdgesWriterWait(t, f, ScanInput{RequestToken: "risk_edges_writer_wait", LookbackHours: 24, Kind: "total", ScanKind: "shared_ips"}, user)
			if count == 100000 {
				ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
				input := ScanInput{RequestToken: "risk_edges_timeout_probe", LookbackHours: 24, Kind: "total", ScanKind: "shared_ips"}
				start := time.Now()
				_, err := f.repository.CreateScan(ctx, actor, input)
				cancel()
				t.Logf("CreateScan cancellation elapsed=%s error=%v", time.Since(start), err)
				if err == nil {
					t.Fatal("expected canceled scan creation")
				}
				var retained int
				recovery, stopRecovery := context.WithTimeout(t.Context(), 2*time.Second)
				err = f.store.DB().QueryRowContext(recovery, `SELECT count(*) FROM risk_client_scans WHERE request_token=?`, input.RequestToken).Scan(&retained)
				stopRecovery()
				if err != nil || retained != 0 {
					t.Fatal("canceled creation retained a scan or writer", retained, err)
				}
			}
		})
	}
}
