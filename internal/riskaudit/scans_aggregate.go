package riskaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Aggregate scans keep exactly one unpublished group. Its source links are
// appended in bounded batches, so a retired contributor removes the group
// before any historical user or IP snapshot can be read.
func (r *Repository) processAggregateScanBatch(ctx context.Context, tx *sql.Tx, scan ClientScan, now int64) (bool, error) {
	var checkpoint scanCheckpoint
	if err := json.Unmarshal([]byte(scan.checkpoint), &checkpoint); err != nil || !checkpoint.Config.Valid() {
		return false, ErrUnavailable
	}
	pending := checkpoint.PendingUser != 0 || checkpoint.PendingIP != ""
	if pending {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM risk_scan_results WHERE scan_id=? AND row_no=? AND published=0)`, scan.ID, scan.Matched+1).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			_, err := tx.ExecContext(ctx, `UPDATE risk_client_scans SET state='failed',reason='source_changed',changed=1,checkpoint_json=`+scanCheckpointWithoutIdentitySQL+`,updated_at=? WHERE id=?`, now, scan.ID)
			if err != nil {
				return false, err
			}
			return true, tx.Commit()
		}
	} else {
		var err error
		if scan.ScanKind == "users" {
			err = startUserGroup(ctx, tx, &scan, &checkpoint)
		} else {
			err = startIPGroup(ctx, tx, &scan, &checkpoint)
		}
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET state='completed',checkpoint_json=`+scanCheckpointWithoutIdentitySQL+`,updated_at=?,failures=0 WHERE id=?`, now, scan.ID)
			if err != nil {
				return false, err
			}
			return true, tx.Commit()
		}
		if err != nil {
			return false, err
		}
	}
	var complete bool
	var err error
	if scan.ScanKind == "users" {
		complete, err = processUserGroup(ctx, tx, &scan, &checkpoint)
	} else {
		complete, err = processIPGroup(ctx, tx, &scan, &checkpoint)
	}
	if err != nil {
		return false, err
	}
	if scan.Scanned >= MaxScanCandidates && scan.State != "limited" {
		// The current group cannot be offered as a partial aggregate.
		if !complete {
			if _, err = tx.ExecContext(ctx, `DELETE FROM risk_scan_results WHERE scan_id=? AND row_no=? AND published=0`, scan.ID, scan.Matched+1); err != nil {
				return false, err
			}
		}
		scan.State, scan.Reason = "limited", "candidate_limit"
	}
	if scan.Matched >= MaxScanResults {
		scan.State, scan.Reason = "limited", "result_limit"
	}
	if scan.State != "limited" {
		scan.State = "running"
	} else {
		checkpoint.PendingUser, checkpoint.AfterUser = 0, 0
		checkpoint.PendingIP, checkpoint.AfterIP = "", ""
		checkpoint.SourceAt, checkpoint.SourceID = 0, 0
		checkpoint.IPSummary = nil
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil || len(encoded) > 16384 {
		return false, ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET state=?,reason=?,scanned=?,matched=?,checkpoint_json=?,updated_at=?,failures=0 WHERE id=?`, scan.State, scan.Reason, scan.Scanned, scan.Matched, string(encoded), now, scan.ID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func startUserGroup(ctx context.Context, tx *sql.Tx, scan *ClientScan, checkpoint *scanCheckpoint) error {
	var userID int64
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM (
 SELECT user_id FROM risk_audit_minutes WHERE minute>=? AND minute<? AND user_id>?
 UNION SELECT user_id FROM request_source_facts WHERE occurred_at>=? AND occurred_at<? AND source_id<=? AND user_id>? AND kind IN ('self','charity','unclassified')
 ) ORDER BY user_id LIMIT 1`, scan.From, scan.To, checkpoint.AfterUser, scan.From, scan.To, scan.upperSource, checkpoint.AfterUser).Scan(&userID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO risk_scan_results(scan_id,row_no,user_id,result_json,published) VALUES(?,?,?,'{}',0)`, scan.ID, scan.Matched+1, userID)
	if err != nil {
		return err
	}
	checkpoint.PendingUser = userID
	checkpoint.SourceAt, checkpoint.SourceID = scan.From, 0
	return nil
}

func processUserGroup(ctx context.Context, tx *sql.Tx, scan *ClientScan, checkpoint *scanCheckpoint) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT request_log_id,source_id,occurred_at FROM request_source_facts WHERE user_id=? AND occurred_at>=? AND occurred_at<? AND source_id<=? AND kind IN ('self','charity','unclassified') AND (occurred_at>? OR (occurred_at=? AND source_id>?)) ORDER BY occurred_at,source_id LIMIT ?`, checkpoint.PendingUser, scan.From, scan.To, scan.upperSource, checkpoint.SourceAt, checkpoint.SourceAt, checkpoint.SourceID, ScanBatchSize)
	if err != nil {
		return false, err
	}
	items := make([]struct{ id, sourceID, at int64 }, 0, ScanBatchSize)
	for rows.Next() {
		var root, sourceID, at int64
		if err = rows.Scan(&root, &sourceID, &at); err != nil {
			rows.Close()
			return false, err
		}
		items = append(items, struct{ id, sourceID, at int64 }{root, sourceID, at})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO risk_scan_result_sources(scan_id,row_no,request_log_id) VALUES(?,?,?)`, scan.ID, scan.Matched+1, item.id); err != nil {
			return false, err
		}
		checkpoint.SourceAt, checkpoint.SourceID = item.at, item.sourceID
		scan.Scanned++
		if scan.Scanned >= MaxScanCandidates {
			break
		}
	}
	if scan.Scanned >= MaxScanCandidates {
		return false, nil
	}
	if len(items) == ScanBatchSize {
		return false, nil
	}
	if checkpoint.SourceID == 0 {
		scan.Scanned++ // A minute-only user is still one examined candidate.
	}
	w := Window{From: scan.From, To: scan.To, Kind: scan.Kind, Limit: MaxPage}
	minutes, truncated, err := readMinutes(ctx, tx, []int64{checkpoint.PendingUser}, w)
	if err != nil {
		return false, err
	}
	if truncated {
		if _, err = tx.ExecContext(ctx, `DELETE FROM risk_scan_results WHERE scan_id=? AND row_no=?`, scan.ID, scan.Matched+1); err != nil {
			return false, err
		}
		scan.State, scan.Reason = "limited", "minute_limit"
		return true, nil
	}
	gaps, err := readGaps(ctx, tx, scan.From, scan.To)
	if err != nil {
		return false, err
	}
	summary := SummarizeMinutes(checkpoint.PendingUser, minutes, gaps, checkpoint.Config, scan.To, scan.Kind)
	matched := checkpoint.Signal == "" || checkpoint.Signal == "rpm" && summary.RPMRisk || checkpoint.Signal == "concurrency" && summary.ConcurrencyRisk
	if matched {
		body, err := json.Marshal(summary)
		if err != nil || len(body) > 16384 {
			return false, ErrUnavailable
		}
		if _, err = tx.ExecContext(ctx, `UPDATE risk_scan_results SET result_json=?,published=1 WHERE scan_id=? AND row_no=? AND published=0`, string(body), scan.ID, scan.Matched+1); err != nil {
			return false, err
		}
		scan.Matched++
	} else if _, err = tx.ExecContext(ctx, `DELETE FROM risk_scan_results WHERE scan_id=? AND row_no=?`, scan.ID, scan.Matched+1); err != nil {
		return false, err
	}
	checkpoint.AfterUser = checkpoint.PendingUser
	checkpoint.PendingUser, checkpoint.SourceAt, checkpoint.SourceID = 0, 0, 0
	return true, nil
}

func startIPGroup(ctx context.Context, tx *sql.Tx, scan *ClientScan, checkpoint *scanCheckpoint) error {
	var ip string
	var firstSource int64
	err := tx.QueryRowContext(ctx, `SELECT s.effective_ip,s.request_log_id FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE effective_ip>? AND occurred_at>=? AND occurred_at<? AND source_id<=? AND l.origin_user_id IS NOT NULL AND l.origin_discord_id IS NOT NULL AND kind IN ('self','charity','unclassified') AND (?='total' OR kind=?) AND ip_quality IN ('direct_peer','trusted_forwarded') ORDER BY effective_ip,occurred_at,request_log_id LIMIT 1`, checkpoint.AfterIP, scan.From, scan.To, scan.upperSource, scan.Kind, scan.Kind).Scan(&ip, &firstSource)
	if err != nil {
		return err
	}
	if normalized, ok := normalizeIP(ip); !ok || normalized != ip {
		return ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO risk_scan_results(scan_id,row_no,request_log_id,result_json,published) VALUES(?,?,?,'{}',0)`, scan.ID, scan.Matched+1, firstSource)
	if err != nil {
		return err
	}
	checkpoint.PendingIP, checkpoint.SourceAt, checkpoint.SourceID = ip, scan.From, 0
	checkpoint.IPSummary = &SharedIP{IP: ip, Associations: []IPAssociation{}}
	return nil
}

func processIPGroup(ctx context.Context, tx *sql.Tx, scan *ClientScan, checkpoint *scanCheckpoint) (bool, error) {
	if checkpoint.IPSummary == nil || checkpoint.IPSummary.IP != checkpoint.PendingIP {
		return false, ErrUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.request_log_id,s.source_id,s.occurred_at,l.origin_user_id,s.kind,EXISTS(SELECT 1 FROM dispatch_claims dc WHERE dc.logical_request_id=l.logical_request_id AND dc.dispatched_at IS NOT NULL),COALESCE(l.caller_result_class,'') FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE s.effective_ip=? AND s.occurred_at>=? AND s.occurred_at<? AND s.source_id<=? AND l.origin_user_id IS NOT NULL AND l.origin_discord_id IS NOT NULL AND s.kind IN ('self','charity','unclassified') AND (?='total' OR s.kind=?) AND s.ip_quality IN ('direct_peer','trusted_forwarded') AND (s.occurred_at>? OR (s.occurred_at=? AND s.source_id>?)) ORDER BY s.occurred_at,s.source_id LIMIT ?`, checkpoint.PendingIP, scan.From, scan.To, scan.upperSource, scan.Kind, scan.Kind, checkpoint.SourceAt, checkpoint.SourceAt, checkpoint.SourceID, ScanBatchSize)
	if err != nil {
		return false, err
	}
	type ipCandidate struct {
		id, sourceID, at, userID int64
		kind, outcome            string
		dispatched               bool
	}
	items := make([]ipCandidate, 0, ScanBatchSize)
	for rows.Next() {
		var item ipCandidate
		if err = rows.Scan(&item.id, &item.sourceID, &item.at, &item.userID, &item.kind, &item.dispatched, &item.outcome); err != nil {
			rows.Close()
			return false, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO risk_scan_result_sources(scan_id,row_no,request_log_id) VALUES(?,?,?)`, scan.ID, scan.Matched+1, item.id); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO risk_scan_result_users(scan_id,row_no,user_id) VALUES(?,?,?)`, scan.ID, scan.Matched+1, item.userID); err != nil {
			return false, err
		}
		summary := checkpoint.IPSummary
		if summary.Requests == 0 || item.at < summary.FirstSeen {
			summary.FirstSeen = item.at
		}
		if item.at > summary.LastSeen {
			summary.LastSeen = item.at
		}
		summary.Requests++
		found := false
		for index := range summary.Associations {
			association := &summary.Associations[index]
			if association.UserID == item.userID && association.Kind == item.kind {
				association.Requests++
				association.LastSeen = max(association.LastSeen, item.at)
				association.FirstSeen = min(association.FirstSeen, item.at)
				if item.dispatched {
					association.Dispatched++
				} else if item.outcome == "failed" {
					association.Rejected++
				}
				found = true
				break
			}
		}
		if !found {
			if len(summary.Associations) < 24 {
				association := IPAssociation{UserID: item.userID, Kind: item.kind, FirstSeen: item.at, LastSeen: item.at, Requests: 1}
				if item.dispatched {
					association.Dispatched = 1
				} else if item.outcome == "failed" {
					association.Rejected = 1
				}
				summary.Associations = append(summary.Associations, association)
			} else {
				summary.AssociationsTruncated = true
			}
		}
		checkpoint.SourceAt, checkpoint.SourceID = item.at, item.sourceID
		scan.Scanned++
		if scan.Scanned >= MaxScanCandidates {
			break
		}
	}
	if scan.Scanned >= MaxScanCandidates || len(items) == ScanBatchSize {
		return false, nil
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(DISTINCT l.origin_discord_id) FROM risk_scan_result_sources c JOIN request_logs l ON l.id=c.request_log_id WHERE c.scan_id=? AND c.row_no=?`, scan.ID, scan.Matched+1).Scan(&checkpoint.IPSummary.Users); err != nil {
		return false, err
	}
	if checkpoint.IPSummary.Users >= checkpoint.Config.SharedIPUsers {
		body, err := json.Marshal(checkpoint.IPSummary)
		if err != nil || len(body) > 16384 {
			return false, ErrUnavailable
		}
		if _, err = tx.ExecContext(ctx, `UPDATE risk_scan_results SET result_json=?,published=1 WHERE scan_id=? AND row_no=? AND published=0`, string(body), scan.ID, scan.Matched+1); err != nil {
			return false, err
		}
		scan.Matched++
	} else if _, err = tx.ExecContext(ctx, `DELETE FROM risk_scan_results WHERE scan_id=? AND row_no=?`, scan.ID, scan.Matched+1); err != nil {
		return false, err
	}
	checkpoint.AfterIP, checkpoint.PendingIP = checkpoint.PendingIP, ""
	checkpoint.SourceAt, checkpoint.SourceID = 0, 0
	checkpoint.IPSummary = nil
	return true, nil
}

type TaskResults struct {
	Scan       ClientScan `json:"scan"`
	Items      any        `json:"items"`
	Page       string     `json:"page"`
	PageSize   int        `json:"page_size"`
	TotalItems string     `json:"total_items"`
	TotalPages string     `json:"total_pages"`
	Coverage   string     `json:"coverage"`
}

func (r *Repository) TaskResults(ctx context.Context, actor Actor, id string, page int64, size int) (TaskResults, error) {
	if page < 1 || page > 2147483647 || (size != 20 && size != 50 && size != 100) {
		return TaskResults{}, ErrInvalid
	}
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return TaskResults{}, err
	}
	defer tx.Rollback()
	scan, err := r.ownedScan(ctx, tx, actor, id, true)
	if err != nil {
		return TaskResults{}, err
	}
	var total int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM risk_scan_results WHERE scan_id=? AND published=1`, id).Scan(&total); err != nil {
		return TaskResults{}, err
	}
	pages := max(1, (total+int64(size)-1)/int64(size))
	page = min(page, pages)
	out := TaskResults{Scan: scan, Page: strconv.FormatInt(page, 10), PageSize: size, TotalItems: strconv.FormatInt(total, 10), TotalPages: strconv.FormatInt(pages, 10), Coverage: scan.Coverage}
	rows, err := tx.QueryContext(ctx, `SELECT result_json,request_log_id FROM risk_scan_results WHERE scan_id=? AND published=1 ORDER BY row_no LIMIT ? OFFSET ?`, id, size, (page-1)*int64(size))
	if err != nil {
		return TaskResults{}, err
	}
	var raw []string
	var sourceIDs []int64
	for rows.Next() {
		var body string
		var source sql.NullInt64
		if err = rows.Scan(&body, &source); err != nil {
			rows.Close()
			return TaskResults{}, err
		}
		raw = append(raw, body)
		if source.Valid {
			sourceIDs = append(sourceIDs, source.Int64)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return TaskResults{}, err
	}
	switch scan.ScanKind {
	case "client_hits":
		items := make([]SourceRequest, 0, len(sourceIDs))
		if len(sourceIDs) > 0 {
			mapped, err := sourcesByID(ctx, tx, sourceIDs)
			if err != nil {
				return TaskResults{}, err
			}
			budget := 100
			for _, sourceID := range sourceIDs {
				if item, ok := mapped[sourceID]; ok {
					attachMatches(&item, scan.rules, &budget)
					items = append(items, item)
				}
			}
		}
		out.Items = items
	case "users":
		items := make([]UserSummary, 0, len(raw))
		for _, body := range raw {
			var item UserSummary
			if json.Unmarshal([]byte(body), &item) != nil {
				return TaskResults{}, ErrUnavailable
			}
			items = append(items, item)
		}
		out.Items = items
	case "shared_ips":
		items := make([]SharedIP, 0, len(raw))
		for _, body := range raw {
			var item SharedIP
			if json.Unmarshal([]byte(body), &item) != nil {
				return TaskResults{}, ErrUnavailable
			}
			items = append(items, item)
		}
		out.Items = items
	case "user_ips":
		items := make([]UserIPs, 0, len(raw))
		for _, body := range raw {
			var item UserIPs
			if json.Unmarshal([]byte(body), &item) != nil {
				return TaskResults{}, ErrUnavailable
			}
			items = append(items, item)
		}
		out.Items = items
	default:
		return TaskResults{}, fmt.Errorf("audit scan kind: %w", ErrUnavailable)
	}
	return out, nil
}
