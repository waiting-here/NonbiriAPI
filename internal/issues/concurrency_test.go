package issues

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestIssueConcurrentObserveCloseAndAccountDeleteConverges(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "concurrent-owner")
	endpointID := environment.seedEndpoint(t, userID)
	keyID := environment.seedEndpointKey(t, endpointID)
	environment.setDiscovery(t, keyID, "failed", "auth", issueTestNow)

	runConcurrent := func(count int, operation func() error) []error {
		start := make(chan struct{})
		errorsByCall := make([]error, count)
		var wait sync.WaitGroup
		wait.Add(count)
		for index := 0; index < count; index++ {
			go func(index int) {
				defer wait.Done()
				<-start
				errorsByCall[index] = operation()
			}(index)
		}
		close(start)
		wait.Wait()
		return errorsByCall
	}
	for index, err := range runConcurrent(16, func() error {
		return environment.service.ReconcileModelDiscovery(context.Background(), userID, keyID)
	}) {
		if err != nil {
			t.Fatalf("concurrent observation %d: %v", index, err)
		}
	}
	current := listIssues(t, environment, userID, "current")
	if len(current.Data) != 1 || current.Data[0].Count != "1" {
		t.Fatalf("concurrent duplicate observations diverged: %+v", current)
	}

	environment.clock.Add(1)
	environment.setDiscovery(t, keyID, "succeeded", "none", environment.clock.Load())
	for index, err := range runConcurrent(8, func() error {
		return environment.service.ReconcileModelDiscovery(context.Background(), userID, keyID)
	}) {
		if err != nil {
			t.Fatalf("concurrent recovery %d: %v", index, err)
		}
	}
	if got := len(listIssues(t, environment, userID, "current").Data); got != 0 {
		t.Fatalf("concurrent recovery left %d current issues", got)
	}
	closed := listIssues(t, environment, userID, "closed")
	if len(closed.Data) != 1 || closed.Data[0].Count != "1" {
		t.Fatalf("concurrent recovery rewrote history: %+v", closed)
	}

	environment.clock.Add(1)
	environment.setDiscovery(t, keyID, "failed", "transport", environment.clock.Load())
	start := make(chan struct{})
	observeErrors := make([]error, 12)
	var deleteErr error
	var wait sync.WaitGroup
	wait.Add(len(observeErrors) + 1)
	for index := range observeErrors {
		go func(index int) {
			defer wait.Done()
			<-start
			observeErrors[index] = environment.service.ReconcileModelDiscovery(context.Background(), userID, keyID)
		}(index)
	}
	go func() {
		defer wait.Done()
		<-start
		deleteErr = deleteIssueAccount(environment, userID)
	}()
	close(start)
	wait.Wait()
	if deleteErr != nil {
		t.Fatalf("concurrent account deletion: %v", deleteErr)
	}
	for index, err := range observeErrors {
		if err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatalf("observe/delete result %d: %v", index, err)
		}
	}
	var users, issueRows, projectionRows, unresolvedAlerts int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM users WHERE id=?`, userID).Scan(&users); err != nil {
		t.Fatalf("count deleted user: %v", err)
	}
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE user_id=?`, userID).Scan(&issueRows); err != nil {
		t.Fatalf("count deleted issue rows: %v", err)
	}
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issue_projection_state WHERE user_id=?`, userID).Scan(&projectionRows); err != nil {
		t.Fatalf("count deleted projection rows: %v", err)
	}
	if err := environment.store.DB().QueryRow(`
SELECT count(*) FROM admin_alerts WHERE kind='issue_projection_incomplete' AND subject_user_id=? AND resolved=0`, userID).Scan(&unresolvedAlerts); err != nil {
		t.Fatalf("count deleted projection alerts: %v", err)
	}
	if users != 0 || issueRows != 0 || projectionRows != 0 || unresolvedAlerts != 0 {
		t.Fatalf("delete convergence users=%d issues=%d projections=%d alerts=%d", users, issueRows, projectionRows, unresolvedAlerts)
	}
}

func TestIssueConcurrentRebuildAndResourceDeleteConverges(t *testing.T) {
	environment := newIssueTestEnvironment(t)
	userID := environment.seedUser(t, "rebuild-delete-owner")
	endpointID := environment.seedEndpoint(t, userID)
	environment.validation.set(userID, ResourceEndpoint, endpointID, RootConfigurationInvalid, ResourceValidationState{
		Active: true, ObservedAt: issueTestNow, SafeDetail: "configuration_invalid",
	})
	if _, err := environment.store.DB().Exec(`
INSERT INTO user_issue_projection_state(user_id,projection_incomplete,rebuild_generation,rebuild_cursor,updated_at)
VALUES(?,1,1,?,?)`, userID, initialRebuildCursor, issueTestNow); err != nil {
		t.Fatalf("seed rebuild/delete checkpoint: %v", err)
	}

	start := make(chan struct{})
	var rebuildErr, deleteErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, rebuildErr = environment.service.RebuildBatch(context.Background(), userID)
	}()
	go func() {
		defer wait.Done()
		<-start
		deleteErr = deleteIssueEndpoint(environment, userID, endpointID)
	}()
	close(start)
	wait.Wait()
	if deleteErr != nil {
		t.Fatalf("concurrent endpoint deletion: %v", deleteErr)
	}
	if rebuildErr != nil && !errors.Is(rebuildErr, ErrNotFound) && !errors.Is(rebuildErr, ErrConflict) {
		t.Fatalf("concurrent rebuild result: %v", rebuildErr)
	}
	environment.validation.remove(userID, ResourceEndpoint, endpointID, RootConfigurationInvalid)
	result, err := environment.service.RebuildBatch(context.Background(), userID)
	if err != nil || !result.Complete {
		t.Fatalf("final rebuild convergence: result=%+v err=%v", result, err)
	}
	var endpointRows, currentRows, incomplete int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM endpoints WHERE id=?`, endpointID).Scan(&endpointRows); err != nil {
		t.Fatalf("count deleted endpoint: %v", err)
	}
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM user_issues WHERE user_id=? AND state='current'`, userID).Scan(&currentRows); err != nil {
		t.Fatalf("count current after rebuild/delete: %v", err)
	}
	if err := environment.store.DB().QueryRow(`SELECT projection_incomplete FROM user_issue_projection_state WHERE user_id=?`, userID).Scan(&incomplete); err != nil {
		t.Fatalf("read final projection state: %v", err)
	}
	if endpointRows != 0 || currentRows != 0 || incomplete != 0 {
		t.Fatalf("rebuild/delete convergence endpoints=%d current=%d incomplete=%d", endpointRows, currentRows, incomplete)
	}
	closed := listIssues(t, environment, userID, "closed")
	for _, issue := range closed.Data {
		if issue.DeepLink != nil || issue.SafeDetail != "" {
			t.Fatalf("deleted resource identity survived: %+v", issue)
		}
	}
}

func deleteIssueAccount(environment *issueTestEnvironment, userID int64) error {
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := environment.service.Sources().PrepareAccountDeletion(context.Background(), tx, userID, environment.clock.Load()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `DELETE FROM users WHERE id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteIssueEndpoint(environment *issueTestEnvironment, userID, endpointID int64) error {
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := environment.service.Sources().PrepareEndpointDeletion(context.Background(), tx, userID, endpointID, environment.clock.Load()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(context.Background(), `DELETE FROM endpoints WHERE id=? AND user_id=?`, endpointID, userID); err != nil {
		return err
	}
	return tx.Commit()
}
