package riskaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
)

const MaxWindowSources = 100000
const MaxWindowBytes = 16 << 20
const windowSourceBytes = 256

type UserIPs struct {
	DiscordID         string          `json:"discord_id"`
	Peak              int             `json:"peak"`
	WindowFrom        int64           `json:"window_from"`
	WindowTo          int64           `json:"window_to"`
	IPs               []string        `json:"ips"`
	IPsTruncated      bool            `json:"ips_truncated"`
	Accounts          []IPAssociation `json:"accounts"`
	AccountsTruncated bool            `json:"accounts_truncated"`
}
type windowSource struct {
	root, sourceID, at, user int64
	ip, kind                 string
}

func (r *Repository) processUserIPsBatch(ctx context.Context, tx *sql.Tx, scan ClientScan, now int64) (bool, error) {
	var cp scanCheckpoint
	if json.Unmarshal([]byte(scan.checkpoint), &cp) != nil || !cp.Config.Valid() {
		return false, ErrUnavailable
	}
	window := int64(cp.Config.UserIPWindowHours) * 3600
	from := max(int64(0), scan.From-window)
	row := scan.Matched + 1
	if cp.PendingDiscord == "" {
		var root int64
		err := tx.QueryRowContext(ctx, `SELECT l.origin_discord_id,s.request_log_id FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE l.origin_discord_id>? AND l.origin_user_id IS NOT NULL AND s.occurred_at>=? AND s.occurred_at<? AND s.source_id<=? AND s.kind IN ('self','charity','unclassified') AND (?='total' OR s.kind=?) AND s.ip_quality IN ('direct_peer','trusted_forwarded') ORDER BY l.origin_discord_id,s.occurred_at,s.request_log_id LIMIT 1`, cp.AfterDiscord, from, scan.To, scan.upperSource, scan.Kind, scan.Kind).Scan(&cp.PendingDiscord, &root)
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
		if _, err = tx.ExecContext(ctx, `INSERT INTO risk_scan_results(scan_id,row_no,request_log_id,published,result_json) VALUES(?,?,?,0,'{}')`, scan.ID, row, root); err != nil {
			return false, err
		}
		cp.SourceAt, cp.SourceID = from, 0
		cp.UserIPSummary = &UserIPs{DiscordID: cp.PendingDiscord, IPs: []string{}, Accounts: []IPAssociation{}}
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM risk_scan_results WHERE scan_id=? AND row_no=? AND published=0)`, scan.ID, row).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, ErrUnavailable
	}
	queue, err := readWindowSources(ctx, tx, scan.ID)
	if err != nil {
		return false, err
	}
	counts := map[string]int{}
	for _, s := range queue {
		counts[s.ip]++
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.request_log_id,s.source_id,s.occurred_at,l.origin_user_id,s.effective_ip,s.kind FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE l.origin_discord_id=? AND l.origin_user_id IS NOT NULL AND s.occurred_at>=? AND s.occurred_at<? AND s.source_id<=? AND s.kind IN ('self','charity','unclassified') AND (?='total' OR s.kind=?) AND s.ip_quality IN ('direct_peer','trusted_forwarded') AND (s.occurred_at>? OR (s.occurred_at=? AND s.source_id>?)) ORDER BY s.occurred_at,s.source_id LIMIT ?`, cp.PendingDiscord, from, scan.To, scan.upperSource, scan.Kind, scan.Kind, cp.SourceAt, cp.SourceAt, cp.SourceID, ScanBatchSize)
	if err != nil {
		return false, err
	}
	items := make([]windowSource, 0, ScanBatchSize)
	for rows.Next() {
		var s windowSource
		if err = rows.Scan(&s.root, &s.sourceID, &s.at, &s.user, &s.ip, &s.kind); err != nil {
			break
		}
		items = append(items, s)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return false, err
	}
	summary := cp.UserIPSummary
	if summary == nil || summary.DiscordID != cp.PendingDiscord {
		return false, ErrUnavailable
	}
	head := 0
	var peakQueue []windowSource
	var peakSource windowSource
	for _, s := range items {
		if err = ctx.Err(); err != nil {
			return false, err
		}
		for head < len(queue) && queue[head].at < s.at-window {
			retired := queue[head]
			head++
			counts[retired.ip]--
			if counts[retired.ip] == 0 {
				delete(counts, retired.ip)
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM risk_scan_window_sources WHERE scan_id=? AND request_log_id=?`, scan.ID, retired.root); err != nil {
				return false, err
			}
		}
		if len(queue)-head >= MaxWindowSources || (len(queue)-head+1)*windowSourceBytes > MaxWindowBytes {
			scan.State, scan.Reason = "limited", "window_limit"
			break
		}
		normalized, ok := normalizeIP(s.ip)
		if !ok {
			return false, ErrUnavailable
		}
		s.ip = normalized
		queue = append(queue, s)
		counts[s.ip]++
		if _, err = tx.ExecContext(ctx, `INSERT INTO risk_scan_window_sources(scan_id,request_log_id) VALUES(?,?)`, scan.ID, s.root); err != nil {
			return false, err
		}
		scan.Scanned++
		cp.SourceAt, cp.SourceID = s.at, s.sourceID
		if s.at >= scan.From && len(counts) >= cp.Config.UserIPMinIPs && len(counts) > summary.Peak {
			summary.Peak = len(counts)
			summary.WindowFrom = max(int64(0), s.at-window)
			summary.WindowTo = s.at
			// Retain the latest peak's slice until this batch ends. Appends and
			// expiry only advance the active queue; they do not mutate its rows.
			peakQueue, peakSource = queue[head:], s
		}
		if scan.Scanned >= MaxScanCandidates {
			scan.State, scan.Reason = "limited", "candidate_limit"
			break
		}
	}
	if peakQueue != nil && scan.State != "limited" {
		peakIPs := make(map[string]bool, summary.Peak)
		summary.Accounts = []IPAssociation{}
		summary.AccountsTruncated = false
		for _, source := range peakQueue {
			peakIPs[source.ip] = true
			found := false
			for i := range summary.Accounts {
				a := &summary.Accounts[i]
				if a.UserID == source.user && a.Kind == source.kind {
					a.Requests++
					a.LastSeen = max(a.LastSeen, source.at)
					found = true
					break
				}
			}
			if !found {
				if len(summary.Accounts) < 24 {
					summary.Accounts = append(summary.Accounts, IPAssociation{UserID: source.user, Kind: source.kind, FirstSeen: source.at, LastSeen: source.at, Requests: 1})
				} else {
					summary.AccountsTruncated = true
				}
			}
		}
		summary.IPs = summary.IPs[:0]
		for ip := range peakIPs {
			summary.IPs = append(summary.IPs, ip)
		}
		sort.Strings(summary.IPs)
		summary.IPsTruncated = len(summary.IPs) > 200
		if summary.IPsTruncated {
			summary.IPs = summary.IPs[:200]
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM risk_scan_result_sources WHERE scan_id=? AND row_no=?`, scan.ID, row); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO risk_scan_result_sources(scan_id,row_no,request_log_id) SELECT ?,?,s.request_log_id FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE l.origin_discord_id=? AND l.origin_user_id IS NOT NULL AND s.occurred_at>=? AND (s.occurred_at<? OR (s.occurred_at=? AND s.source_id<=?)) AND s.source_id<=? AND s.kind IN ('self','charity','unclassified') AND (?='total' OR s.kind=?) AND s.ip_quality IN ('direct_peer','trusted_forwarded')`, scan.ID, row, cp.PendingDiscord, summary.WindowFrom, peakSource.at, peakSource.at, peakSource.sourceID, scan.upperSource, scan.Kind, scan.Kind); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE risk_scan_results SET request_log_id=? WHERE scan_id=? AND row_no=?`, peakSource.root, scan.ID, row); err != nil {
			return false, err
		}
	}
	lastSourceAt, lastSourceID := cp.SourceAt, cp.SourceID
	complete := len(items) < ScanBatchSize && scan.State != "limited"
	if complete {
		if summary.Peak >= cp.Config.UserIPMinIPs {
			body, e := json.Marshal(summary)
			if e != nil || len(body) > 16384 {
				return false, ErrUnavailable
			}
			if _, err = tx.ExecContext(ctx, `UPDATE risk_scan_results SET result_json=?,published=1 WHERE scan_id=? AND row_no=?`, string(body), scan.ID, row); err != nil {
				return false, err
			}
			scan.Matched++
		} else {
			if _, err = tx.ExecContext(ctx, `DELETE FROM risk_scan_results WHERE scan_id=? AND row_no=?`, scan.ID, row); err != nil {
				return false, err
			}
		}
		cp.AfterDiscord, cp.PendingDiscord = cp.PendingDiscord, ""
		cp.UserIPSummary = nil
		cp.SourceAt, cp.SourceID = 0, 0
	}
	if scan.Matched >= MaxScanResults {
		scan.State, scan.Reason = "limited", "result_limit"
	}
	if complete || scan.State == "limited" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM risk_scan_window_sources WHERE scan_id=?`, scan.ID); err != nil {
			return false, err
		}
	}
	if scan.State == "limited" {
		if _, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET coverage_json=json_set(coverage_json,'$.last_source_at',?,'$.last_source_id',?) WHERE id=?`, lastSourceAt, lastSourceID, scan.ID); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM risk_scan_results WHERE scan_id=? AND published=0`, scan.ID); err != nil {
			return false, err
		}
		cp.PendingDiscord, cp.AfterDiscord = "", ""
		cp.UserIPSummary = nil
		cp.SourceAt, cp.SourceID = 0, 0
	} else {
		scan.State = "running"
	}
	body, err := json.Marshal(cp)
	if err != nil || len(body) > 16384 {
		return false, ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET state=?,reason=?,scanned=?,matched=?,checkpoint_json=?,updated_at=?,failures=0 WHERE id=?`, scan.State, scan.Reason, scan.Scanned, scan.Matched, string(body), now, scan.ID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func readWindowSources(ctx context.Context, tx *sql.Tx, id string) ([]windowSource, error) {
	rows, err := tx.QueryContext(ctx, `SELECT s.request_log_id,s.source_id,s.occurred_at,l.origin_user_id,s.effective_ip,s.kind FROM risk_scan_window_sources w JOIN request_source_facts s ON s.request_log_id=w.request_log_id JOIN request_logs l ON l.id=s.request_log_id WHERE w.scan_id=? ORDER BY s.occurred_at,s.source_id LIMIT ?`, id, MaxWindowSources+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []windowSource{}
	for rows.Next() {
		var s windowSource
		if err = rows.Scan(&s.root, &s.sourceID, &s.at, &s.user, &s.ip, &s.kind); err != nil {
			return nil, err
		}
		var ok bool
		s.ip, ok = normalizeIP(s.ip)
		if !ok {
			return nil, ErrUnavailable
		}
		out = append(out, s)
		if len(out) > MaxWindowSources {
			return nil, ErrUnavailable
		}
	}
	return out, rows.Err()
}
