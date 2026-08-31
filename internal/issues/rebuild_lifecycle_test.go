package issues

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"
)

type emptyFirstValidationAuthority struct {
	base ResourceValidationAuthority
}

func (authority *emptyFirstValidationAuthority) Current(ctx context.Context, tx *sql.Tx, userID int64, kind ResourceKind, resourceID int64, root RootCause) (ResourceValidationState, error) {
	return authority.base.Current(ctx, tx, userID, kind, resourceID, root)
}

func (authority *emptyFirstValidationAuthority) Scan(ctx context.Context, tx *sql.Tx, userID int64, cursor string, limit int) (ResourceValidationBatch, error) {
	if cursor == "" {
		return ResourceValidationBatch{NextCursor: "after-empty"}, nil
	}
	if cursor != "after-empty" {
		return ResourceValidationBatch{}, errors.New("unexpected provider cursor")
	}
	return authority.base.Scan(ctx, tx, userID, "", limit)
}

type faultingMissingResourceRows struct {
	iterationErr error
	closeErr     error
	closed       bool
	errChecked   bool
}

func (*faultingMissingResourceRows) Next() bool        { return false }
func (*faultingMissingResourceRows) Scan(...any) error { return nil }
func (rows *faultingMissingResourceRows) Close() error { rows.closed = true; return rows.closeErr }
func (rows *faultingMissingResourceRows) Err() error {
	rows.errChecked = true
	return rows.iterationErr
}

func TestCollectMissingResourceCandidatesPropagatesIterationAndCloseErrors(t *testing.T) {
	iterationErr := errors.New("injected iteration failure")
	closeErr := errors.New("injected close failure")
	tests := []struct {
		name           string
		rows           *faultingMissingResourceRows
		want           error
		wantErrChecked bool
	}{
		{name: "iteration", rows: &faultingMissingResourceRows{iterationErr: iterationErr}, want: iterationErr, wantErrChecked: true},
		{name: "close", rows: &faultingMissingResourceRows{closeErr: closeErr}, want: closeErr},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := collectMissingResourceCandidates(testCase.rows, "missing test"); !errors.Is(err, testCase.want) {
				t.Fatalf("collectMissingResourceCandidates error=%v, want wrapped %v", err, testCase.want)
			}
			if !testCase.rows.closed {
				t.Fatal("rows were not closed")
			}
			if testCase.rows.errChecked != testCase.wantErrChecked {
				t.Fatalf("Err checked=%v, want %v", testCase.rows.errChecked, testCase.wantErrChecked)
			}
		})
	}
}

func TestIssueOverflowSetsOneDurableCheckpointAndAlert(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "overflow-owner")
	endpointID := environment.seedEndpoint(t, userID)
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin overflow seed: %v", err)
	}
	statement, err := tx.Prepare(`
INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 first_seen_at,last_seen_at,count
) VALUES(?,?,'resource_validator','endpoint',?,'configuration_invalid',1,'current','configuration_invalid','',?,?,1)`)
	if err != nil {
		t.Fatalf("prepare overflow seed: %v", err)
	}
	for index := int64(0); index < maxCurrentPerUser; index++ {
		if _, err := statement.Exec(issueOpaqueID("iss_", uint64(index+1)), userID,
			strconv.FormatInt(100_000+index, 10), issueTestNow, issueTestNow); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			t.Fatalf("seed current issue %d: %v", index, err)
		}
	}
	if err := statement.Close(); err != nil {
		t.Fatalf("close overflow seed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit overflow seed: %v", err)
	}

	environment.validation.set(userID, ResourceEndpoint, endpointID, RootCredentialInvalid, ResourceValidationState{
		Active: true, ObservedAt: issueTestNow, SafeDetail: "credential_rejected",
	})
	for attempt := 0; attempt < 2; attempt++ {
		if err := environment.service.ReconcileResourceValidation(context.Background(), userID, ResourceEndpoint, endpointID, RootCredentialInvalid); err != nil {
			t.Fatalf("overflow observation %d blocked authority: %v", attempt, err)
		}
	}
	capacityLimited := false
	withIssueTx(t, environment, func(tx *sql.Tx) error {
		return environment.repository.reconcileTx(context.Background(), tx, sourceObservation{
			userID: userID, source: SourceResourceValidator, resourceKind: ResourceEndpoint,
			resourceID: endpointID, rootCause: RootCredentialInvalid, active: true,
			observedAt: issueTestNow, safeDetail: "credential_rejected", capacityLimited: &capacityLimited,
		}, issueTestNow)
	})
	if !capacityLimited {
		t.Fatal("rebuild capacity tracker was not marked")
	}
	var current, projected, incomplete, generation, alerts int64
	var cursor sql.NullString
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE user_id=? AND state='current'`, userID).Scan(&current); err != nil {
		t.Fatalf("count current overflow projection: %v", err)
	}
	if err := environment.store.DB().QueryRow(`
SELECT count(*) FROM user_issues
WHERE user_id=? AND source='resource_validator' AND resource_kind='endpoint' AND resource_ref=? AND root_cause='credential_invalid'`,
		userID, strconv.FormatInt(endpointID, 10)).Scan(&projected); err != nil {
		t.Fatalf("count dropped overflow identity: %v", err)
	}
	if err := environment.store.DB().QueryRow(`
SELECT projection_incomplete,rebuild_generation,rebuild_cursor
FROM user_issue_projection_state WHERE user_id=?`, userID).Scan(&incomplete, &generation, &cursor); err != nil {
		t.Fatalf("read overflow checkpoint: %v", err)
	}
	if err := environment.store.DB().QueryRow(`
SELECT count(*) FROM admin_alerts
WHERE kind='issue_projection_incomplete' AND subject_user_id=? AND resolved=0`, userID).Scan(&alerts); err != nil {
		t.Fatalf("count overflow alerts: %v", err)
	}
	if current != maxCurrentPerUser || projected != 0 || incomplete != 1 || generation != 1 ||
		!cursor.Valid || cursor.String != initialRebuildCursor || alerts != 1 {
		t.Fatalf("overflow current=%d projected=%d incomplete=%d generation=%d cursor=%+v alerts=%d",
			current, projected, incomplete, generation, cursor, alerts)
	}
}

func TestIssueRebuildUsesBoundedDurableCursorAndClearsOnlyAfterCoverage(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "rebuild-owner")
	const targetCount = maxPageLimit + 1
	for index := 0; index < targetCount; index++ {
		endpointID := environment.seedEndpoint(t, userID)
		environment.validation.set(userID, ResourceEndpoint, endpointID, RootConfigurationInvalid, ResourceValidationState{
			Active: true, ObservedAt: issueTestNow, SafeDetail: fmt.Sprintf("configuration_%03d", index),
		})
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO user_issue_projection_state(user_id,projection_incomplete,rebuild_generation,rebuild_cursor,updated_at)
VALUES(?,1,7,?,?)`, userID, initialRebuildCursor, issueTestNow); err != nil {
		t.Fatalf("seed rebuild checkpoint: %v", err)
	}
	if _, err := environment.store.DB().Exec(`
INSERT INTO admin_alerts(kind,message,ref,subject_user_id,created_at,resolved)
VALUES('issue_projection_incomplete','Issue projection is incomplete for one account.','',?,?,0)`, userID, issueTestNow); err != nil {
		t.Fatalf("seed rebuild alert: %v", err)
	}

	first, err := environment.service.RebuildBatch(context.Background(), userID)
	if err != nil {
		t.Fatalf("first rebuild batch: %v", err)
	}
	if first.Generation != 7 || first.Processed != maxPageLimit || first.Complete {
		t.Fatalf("first rebuild result: %+v", first)
	}
	var incomplete int
	var cursor sql.NullString
	if err := environment.store.DB().QueryRow(`
SELECT projection_incomplete,rebuild_cursor FROM user_issue_projection_state WHERE user_id=?`, userID).Scan(&incomplete, &cursor); err != nil {
		t.Fatalf("read first rebuild checkpoint: %v", err)
	}
	if incomplete != 1 || !cursor.Valid || cursor.String == initialRebuildCursor || cursor.String == "" {
		t.Fatalf("first rebuild checkpoint incomplete=%d cursor=%+v", incomplete, cursor)
	}

	repository, err := NewRepository(Config{
		Store: environment.store, CursorKeys: environment.vault, ResourceValidation: environment.validation,
		Now: func() time.Time { return time.Unix(environment.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatalf("restart NewRepository: %v", err)
	}
	restarted, err := NewService(repository)
	if err != nil {
		t.Fatalf("restart NewService: %v", err)
	}
	second, err := restarted.RebuildBatch(context.Background(), userID)
	if err != nil {
		t.Fatalf("second rebuild batch: %v", err)
	}
	if second.Generation != 7 || second.Processed != 1 || !second.Complete {
		t.Fatalf("second rebuild result: %+v", second)
	}
	var current, unresolved int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE user_id=? AND state='current'`, userID).Scan(&current); err != nil {
		t.Fatalf("count rebuilt current: %v", err)
	}
	if err := environment.store.DB().QueryRow(`
SELECT projection_incomplete,rebuild_cursor FROM user_issue_projection_state WHERE user_id=?`, userID).Scan(&incomplete, &cursor); err != nil {
		t.Fatalf("read completed rebuild checkpoint: %v", err)
	}
	if err := environment.store.DB().QueryRow(`
SELECT count(*) FROM admin_alerts WHERE kind='issue_projection_incomplete' AND subject_user_id=? AND resolved=0`, userID).Scan(&unresolved); err != nil {
		t.Fatalf("count unresolved rebuild alerts: %v", err)
	}
	if current != targetCount || incomplete != 0 || cursor.Valid || unresolved != 0 {
		t.Fatalf("completed rebuild current=%d incomplete=%d cursor=%+v unresolved=%d", current, incomplete, cursor, unresolved)
	}
	var nonUnitCounts int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE user_id=? AND count<>1`, userID).Scan(&nonUnitCounts); err != nil || nonUnitCounts != 0 {
		t.Fatalf("rebuild manufactured occurrence counts=%d err=%v", nonUnitCounts, err)
	}
}

func TestIssueRebuildYieldsAfterEmptyAdvancingProviderPage(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "rebuild-empty-page")
	endpointID := environment.seedEndpoint(t, userID)
	environment.validation.set(userID, ResourceEndpoint, endpointID, RootConfigurationInvalid, ResourceValidationState{
		Active: true, ObservedAt: issueTestNow, SafeDetail: "configuration_invalid",
	})
	if _, err := environment.store.DB().Exec(`
INSERT INTO user_issue_projection_state(user_id,projection_incomplete,rebuild_generation,rebuild_cursor,updated_at)
VALUES(?,1,3,'resource_validator:',?)`, userID, issueTestNow); err != nil {
		t.Fatalf("seed validation checkpoint: %v", err)
	}
	repository, err := NewRepository(Config{
		Store: environment.store, CursorKeys: environment.vault,
		ResourceValidation: &emptyFirstValidationAuthority{base: environment.validation},
		Now:                func() time.Time { return time.Unix(environment.clock.Load(), 0) },
	})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	first, err := service.RebuildBatch(context.Background(), userID)
	if err != nil || first.Processed != 0 || first.Complete {
		t.Fatalf("empty provider page did not yield: result=%+v err=%v", first, err)
	}
	var cursor string
	if err := environment.store.DB().QueryRow(`SELECT rebuild_cursor FROM user_issue_projection_state WHERE user_id=?`, userID).Scan(&cursor); err != nil {
		t.Fatalf("read yielded cursor: %v", err)
	}
	if cursor == "resource_validator:" {
		t.Fatalf("empty provider page did not persist progress: %q", cursor)
	}

	second, err := service.RebuildBatch(context.Background(), userID)
	if err != nil || second.Processed != 1 || !second.Complete {
		t.Fatalf("resumed provider page did not complete: result=%+v err=%v", second, err)
	}
	if current := listIssues(t, environment, userID, "current"); len(current.Data) != 1 {
		t.Fatalf("resumed provider projection: %+v", current)
	}
}

func TestIssueClosedRetentionAgeCapacityAndCurrentSurvival(t *testing.T) {
	t.Run("age boundary", func(t *testing.T) {
		environment := newIssueTestEnvironment(t)
		userID := environment.seedUser(t, "retention-age")
		closedAt := issueTestNow - closedRetention
		if _, err := environment.store.DB().Exec(`
INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 first_seen_at,last_seen_at,count,closed_at,retain_until
) VALUES(?,?,'resource_validator','endpoint','8001','credential_invalid',1,'closed','credential_invalid','',0,0,1,?,?)`,
			issueOpaqueID("iss_", 8001), userID, closedAt, issueTestNow); err != nil {
			t.Fatalf("seed expired closed issue: %v", err)
		}
		if _, err := environment.store.DB().Exec(`
INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 first_seen_at,last_seen_at,count
) VALUES(?,?,'resource_validator','endpoint','8002','configuration_invalid',1,'current','configuration_invalid','',0,0,1)`,
			issueOpaqueID("iss_", 8002), userID); err != nil {
			t.Fatalf("seed old current issue: %v", err)
		}
		retained, err := environment.service.RetainClosed(context.Background(), 1)
		if err != nil || retained.ClosedDeleted != 1 {
			t.Fatalf("RetainClosed: result=%+v err=%v", retained, err)
		}
		var current int
		if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE user_id=? AND state='current'`, userID).Scan(&current); err != nil || current != 1 {
			t.Fatalf("age retention removed current: count=%d err=%v", current, err)
		}
	})

	t.Run("per-account cap", func(t *testing.T) {
		environment := newIssueTestEnvironment(t)
		userID := environment.seedUser(t, "retention-cap")
		tx, err := environment.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("begin cap seed: %v", err)
		}
		statement, err := tx.Prepare(`
INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 first_seen_at,last_seen_at,count,closed_at,retain_until
) VALUES(?,?,'resource_validator','endpoint',?,'credential_invalid',1,'closed','credential_invalid','',?,?,1,?,?)`)
		if err != nil {
			t.Fatalf("prepare cap seed: %v", err)
		}
		for index := int64(0); index < maxClosedPerUser; index++ {
			closedAt := issueTestNow - 100 - index
			if _, err := statement.Exec(issueOpaqueID("iss_", uint64(index+1)), userID, strconv.FormatInt(20_000+index, 10),
				closedAt, closedAt, closedAt, closedAt+closedRetention); err != nil {
				t.Fatalf("seed closed issue %d: %v", index, err)
			}
		}
		if err := statement.Close(); err != nil {
			t.Fatalf("close cap seed: %v", err)
		}
		currentID := issueOpaqueID("iss_", 50_000)
		if _, err := tx.Exec(`
INSERT INTO user_issues(
 id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
 first_seen_at,last_seen_at,count
) VALUES(?,?,'resource_validator','endpoint','50000','configuration_invalid',1,'current','configuration_invalid','',?,?,1)`,
			currentID, userID, issueTestNow, issueTestNow); err != nil {
			t.Fatalf("seed current for cap close: %v", err)
		}
		if _, err := closeCurrentIdentity(context.Background(), tx, userID, SourceResourceValidator, ResourceEndpoint, "50000", RootConfigurationInvalid, issueTestNow); err != nil {
			t.Fatalf("close cap issue: %v", err)
		}
		if err := trimClosedTx(context.Background(), tx, userID); err != nil {
			t.Fatalf("trim cap: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit cap: %v", err)
		}
		var count, newest int
		if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE user_id=? AND state='closed'`, userID).Scan(&count); err != nil {
			t.Fatalf("count capped closed issues: %v", err)
		}
		if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE id=?`, currentID).Scan(&newest); err != nil {
			t.Fatalf("check newest closed issue: %v", err)
		}
		if int64(count) != maxClosedPerUser || newest != 1 {
			t.Fatalf("closed cap count=%d newest=%d", count, newest)
		}
	})
}
