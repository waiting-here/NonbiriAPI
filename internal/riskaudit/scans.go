package riskaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	ScanLifetime     = 24 * time.Hour
	ScanBatchSize    = 100
	MaxScanResults   = 100000
	MaxRetainedScans = 32
	MaxQueuedScans   = 8
)

var scanTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// A request token makes a repeated creation safe after a lost HTTP response.
// Relative windows resolve once, when the first creation commits.
type ScanInput struct {
	RequestToken  string `json:"request_token"`
	From          int64  `json:"from,omitempty"`
	To            int64  `json:"to,omitempty"`
	LookbackHours int64  `json:"lookback_hours,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Model         string `json:"model,omitempty"`
}

type ClientScan struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Reason     string `json:"reason"`
	From       int64  `json:"from"`
	To         int64  `json:"to"`
	Kind       string `json:"kind"`
	Model      string `json:"model"`
	Candidates int64  `json:"candidates,string"`
	Scanned    int64  `json:"scanned,string"`
	Matched    int    `json:"matched"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	ExpiresAt  int64  `json:"expires_at"`
	RuleCount  int    `json:"rule_count"`
	owner      int64
	admin      bool
	upper      int64
	afterAt    int64
	afterID    int64
	rules      []Rule
}

const scanCommonColumns = `id,user_id,admin,state,reason,from_at,to_at,call_kind,model,upper_log_id,after_at,after_log_id,candidates,scanned,matched,created_at,updated_at,expires_at`
const scanColumns = scanCommonColumns + `,rules_json`
const scanMetaColumns = scanCommonColumns + `,json_array_length(rules_json)`

func readScan(row scanner) (ClientScan, error) {
	return readScanRow(row, true)
}
func readScanRow(row scanner, hydrate bool) (ClientScan, error) {
	var out ClientScan
	var raw string
	var ruleValue any = &raw
	if !hydrate {
		ruleValue = &out.RuleCount
	}
	err := row.Scan(&out.ID, &out.owner, &out.admin, &out.State, &out.Reason, &out.From, &out.To, &out.Kind, &out.Model, &out.upper, &out.afterAt, &out.afterID, &out.Candidates, &out.Scanned, &out.Matched, &out.CreatedAt, &out.UpdatedAt, &out.ExpiresAt, ruleValue)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if !hydrate {
		return out, nil
	}
	if len(raw) > 16<<20 || json.Unmarshal([]byte(raw), &out.rules) != nil || len(out.rules) > MaxRules {
		return out, ErrUnavailable
	}
	for _, rule := range out.rules {
		if validateRule(rule) != nil || !rule.Enabled {
			return out, ErrUnavailable
		}
	}
	out.RuleCount = len(out.rules)
	return out, nil
}

func (r *Repository) CreateScan(ctx context.Context, actor Actor, input ScanInput) (ClientScan, error) {
	if !scanTokenPattern.MatchString(input.RequestToken) || input.LookbackHours < 0 || input.LookbackHours > 720 || (input.LookbackHours > 0 && (input.From != 0 || input.To != 0)) {
		return ClientScan{}, ErrInvalid
	}
	if input.Kind == "" {
		input.Kind = "total"
	}
	encoded, _ := json.Marshal(input)
	tx, err := r.begin(ctx, actor, true)
	if err != nil {
		return ClientScan{}, err
	}
	defer tx.Rollback()
	var priorID, priorQuery string
	var priorExpiry int64
	err = tx.QueryRowContext(ctx, `SELECT id,query_json,expires_at FROM risk_client_scans WHERE user_id=? AND request_token=?`, actor.UserID, input.RequestToken).Scan(&priorID, &priorQuery, &priorExpiry)
	if err == nil {
		if priorQuery != string(encoded) || priorExpiry <= r.now().Unix() {
			return ClientScan{}, ErrConflict
		}
		return r.ownedScan(ctx, tx, actor, priorID, false)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ClientScan{}, err
	}
	now := r.now().Unix()
	w := Window{From: input.From, To: input.To, Kind: input.Kind, Model: input.Model, Limit: ScanBatchSize}
	if input.LookbackHours > 0 {
		w.From, w.To = max(0, now-input.LookbackHours*3600), now
	}
	w, err = w.validate(now)
	if err != nil {
		return ClientScan{}, err
	}
	var retained, queued, unfinished int
	if err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(state='queued'),0),COALESCE(sum(user_id=? AND state IN ('queued','running')),0) FROM risk_client_scans`, actor.UserID).Scan(&retained, &queued, &unfinished); err != nil {
		return ClientScan{}, err
	}
	if retained >= MaxRetainedScans || queued >= MaxQueuedScans || unfinished > 0 {
		return ClientScan{}, ErrConflict
	}
	rules, err := rulesTx(ctx, tx)
	if err != nil {
		return ClientScan{}, err
	}
	frozen := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		// Actor identities are not needed to reproduce rule matching.
		rule.CreatedByUserID, rule.UpdatedByUserID = nil, nil
		frozen = append(frozen, rule)
	}
	raw, err := json.Marshal(frozen)
	if err != nil || len(raw) > 16<<20 {
		return ClientScan{}, ErrUnavailable
	}
	var upper, total int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(request_log_id),0) FROM request_source_facts`).Scan(&upper); err != nil {
		return ClientScan{}, err
	}
	where, args := scanPredicate(w.From, w.To, upper)
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM request_source_facts s WHERE `+where, args...).Scan(&total); err != nil {
		return ClientScan{}, err
	}
	id, err := db.GenerateOpaqueID("scn_")
	if err != nil {
		return ClientScan{}, err
	}
	state := "queued"
	if total == 0 || len(frozen) == 0 {
		state = "completed"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,created_at,updated_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, actor.UserID, actor.Admin, input.RequestToken, string(encoded), string(raw), state, w.From, w.To, w.Kind, w.Model, upper, w.From, total, now, now, now+int64(ScanLifetime/time.Second))
	if err != nil {
		return ClientScan{}, err
	}
	out, err := readScan(tx.QueryRowContext(ctx, `SELECT `+scanColumns+` FROM risk_client_scans WHERE id=?`, id))
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func scanPredicate(from, to, upper int64) (string, []any) {
	where := `s.user_id IS NOT NULL AND s.kind IN ('self','charity','unclassified') AND s.occurred_at>=? AND s.occurred_at<? AND s.request_log_id<=?`
	args := []any{from, to, upper}
	return where, args
}

func (r *Repository) ownedScan(ctx context.Context, tx *sql.Tx, actor Actor, id string, hydrate bool) (ClientScan, error) {
	if !db.ValidateOpaqueID(id, "scn_") {
		return ClientScan{}, ErrInvalid
	}
	columns := scanMetaColumns
	if hydrate {
		columns = scanColumns
	}
	return readScanRow(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM risk_client_scans WHERE id=? AND user_id=? AND admin=? AND expires_at>? AND reason<>'permission_changed'`, id, actor.UserID, actor.Admin, r.now().Unix()), hydrate)
}

func (r *Repository) GetScan(ctx context.Context, actor Actor, id string) (ClientScan, error) {
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return ClientScan{}, err
	}
	defer tx.Rollback()
	return r.ownedScan(ctx, tx, actor, id, false)
}

func (r *Repository) RecentScans(ctx context.Context, actor Actor) ([]ClientScan, error) {
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+scanMetaColumns+` FROM risk_client_scans WHERE user_id=? AND admin=? AND expires_at>? AND reason<>'permission_changed' ORDER BY created_at DESC,id DESC LIMIT 10`, actor.UserID, actor.Admin, r.now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ClientScan, 0)
	for rows.Next() {
		item, err := readScanRow(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Repository) CancelScan(ctx context.Context, actor Actor, id string) (ClientScan, error) {
	tx, err := r.begin(ctx, actor, true)
	if err != nil {
		return ClientScan{}, err
	}
	defer tx.Rollback()
	out, err := r.ownedScan(ctx, tx, actor, id, false)
	if err != nil {
		return out, err
	}
	if out.State == "queued" || out.State == "running" {
		out.State, out.UpdatedAt = "cancelled", r.now().Unix()
		if _, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET state='cancelled',updated_at=? WHERE id=?`, out.UpdatedAt, id); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

type ScanResults struct {
	Scan       ClientScan      `json:"scan"`
	Items      []SourceRequest `json:"items"`
	Page       string          `json:"page"`
	PageSize   int             `json:"page_size"`
	TotalItems string          `json:"total_items"`
	TotalPages string          `json:"total_pages"`
}

func (r *Repository) ScanResults(ctx context.Context, actor Actor, id string, page int64, size int) (ScanResults, error) {
	out := ScanResults{Items: make([]SourceRequest, 0)}
	if page < 1 || page > 2147483647 || (size != 10 && size != 20 && size != 50 && size != 100) {
		return out, ErrInvalid
	}
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Scan, err = r.ownedScan(ctx, tx, actor, id, true)
	if err != nil {
		return out, err
	}
	var total int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM risk_client_scan_matches WHERE scan_id=?`, id).Scan(&total); err != nil {
		return out, err
	}
	pages := max(1, (total+int64(size)-1)/int64(size))
	page = min(page, pages)
	out.Page, out.PageSize, out.TotalItems, out.TotalPages = strconv.FormatInt(page, 10), size, strconv.FormatInt(total, 10), strconv.FormatInt(pages, 10)
	rows, err := tx.QueryContext(ctx, `SELECT request_log_id FROM risk_client_scan_matches WHERE scan_id=? ORDER BY ordinal LIMIT ? OFFSET ?`, id, size, (page-1)*int64(size))
	if err != nil {
		return out, err
	}
	ids := make([]int64, 0, size)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	// Each retained reference resolves through the ordinary source/log rows;
	// deletion and log retention therefore cannot revive historical identities.
	items, err := sourcesByID(ctx, tx, ids)
	if err != nil {
		return out, err
	}
	budget := 100
	for _, id := range ids {
		if item, ok := items[id]; ok {
			matches := matchRules(item.Source, out.Scan.rules, false)
			item.MatchCount = len(matches)
			n := min(len(matches), 20, budget)
			item.Matches, item.MatchesTruncated = matches[:n], n < len(matches)
			budget -= n
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

func sourcesByID(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]SourceRequest, error) {
	args := make([]any, len(ids))
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		placeholders[i] = "?"
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.request_log_id,s.user_id,l.logical_request_id,s.kind,s.occurred_at,s.source_json,s.effective_ip,s.ip_quality,l.model,COALESCE(l.caller_result_class,'running'),EXISTS(SELECT 1 FROM dispatch_claims dc WHERE dc.logical_request_id=l.logical_request_id AND dc.dispatched_at IS NOT NULL),l.error_code,COALESCE(l.rejection_reason,''),l.duration_ms,l.completed_at FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE s.user_id IS NOT NULL AND s.request_log_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]SourceRequest, len(ids))
	for rows.Next() {
		var item SourceRequest
		var raw, ip, quality string
		var duration int64
		var completed sql.NullInt64
		if err = rows.Scan(&item.LogID, &item.UserID, &item.RequestID, &item.Kind, &item.OccurredAt, &raw, &ip, &quality, &item.Model, &item.Outcome, &item.Dispatched, &item.ErrorCode, &item.RejectionReason, &duration, &completed); err != nil {
			return nil, err
		}
		item.Source, err = decodeSource(raw)
		if err != nil {
			item.Source = Source{Quality: map[string]FieldQuality{"source": {Invalid: true}}}
		}
		item.Source.EffectiveIP, item.Source.IPQuality = ip, quality
		if completed.Valid {
			item.DurationMillis = &duration
		}
		out[item.LogID] = item
	}
	return out, rows.Err()
}

// ProcessScanBatch commits at most one batch. A cancelled transaction leaves
// its previous checkpoint intact, including when the service is shutting down.
func (r *Repository) ProcessScanBatch(ctx context.Context) (progress bool, result error) {
	parent := ctx
	if !r.scanWorker.TryLock() {
		return false, nil
	}
	defer r.scanWorker.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	scanID := ""
	defer func() {
		if result == nil || parent.Err() != nil || scanID == "" {
			return
		}
		_ = tx.Rollback()
		bounded, cancel := context.WithTimeout(parent, 2*time.Second)
		defer cancel()
		_, _ = r.db.ExecContext(bounded, `UPDATE risk_client_scans SET failures=failures+1,state=CASE WHEN failures>=2 THEN 'failed' ELSE state END,reason=CASE WHEN failures>=2 THEN 'scan_failed' ELSE reason END WHERE id=? AND state IN ('queued','running')`, scanID)
	}()
	now := r.now().Unix()
	scan, err := readScan(tx.QueryRowContext(ctx, `SELECT `+scanColumns+` FROM risk_client_scans WHERE state IN ('queued','running') AND expires_at>? ORDER BY state DESC,created_at,id LIMIT 1`, now))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	scanID = scan.ID
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND ((?=1 AND is_admin=1) OR (?=0 AND is_admin=0 AND COALESCE(level,auto_level)=6)) AND (is_banned=0 OR (banned_until IS NOT NULL AND banned_until<=?)))`, scan.owner, scan.admin, scan.admin, now).Scan(&allowed)
	if err != nil {
		return false, err
	}
	if !allowed {
		_, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET state='cancelled',reason='permission_changed',updated_at=? WHERE id=?`, now, scan.ID)
		if err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	items, err := readScanCandidates(ctx, tx, scan)
	if err != nil {
		return false, err
	}
	scan.State = "running"
	for _, item := range items {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		scan.Scanned++
		scan.afterAt, scan.afterID = item.at, item.id
		if (scan.Kind != "total" && item.kind != scan.Kind) || (scan.Model != "" && item.model != scan.Model) {
			continue
		}
		if len(matchRules(item.source, scan.rules, false)) > 0 {
			scan.Matched++
			if _, err = tx.ExecContext(ctx, `INSERT INTO risk_client_scan_matches(scan_id,request_log_id,ordinal) VALUES(?,?,?)`, scan.ID, item.id, scan.Matched); err != nil {
				return false, err
			}
			if scan.Matched == MaxScanResults {
				scan.State, scan.Reason = "limited", "result_limit"
				break
			}
		}
	}
	if scan.State == "running" && len(items) < ScanBatchSize {
		scan.State = "completed"
	}
	_, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET state=?,reason=?,after_at=?,after_log_id=?,scanned=?,matched=?,updated_at=?,failures=0 WHERE id=?`, scan.State, scan.Reason, scan.afterAt, scan.afterID, scan.Scanned, scan.Matched, now, scan.ID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

type scanCandidate struct {
	id, at      int64
	source      Source
	kind, model string
}

const scanCandidateSelect = `SELECT s.request_log_id,s.occurred_at,s.source_json,s.effective_ip,s.ip_quality,s.kind,l.model FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE s.user_id IS NOT NULL AND s.kind IN ('self','charity','unclassified')`
const scanSameSecond = scanCandidateSelect + ` AND s.occurred_at=? AND s.request_log_id>? AND s.request_log_id<=? ORDER BY s.request_log_id LIMIT ?`
const scanLaterSeconds = scanCandidateSelect + ` AND s.occurred_at>? AND s.occurred_at<? AND s.request_log_id<=? ORDER BY s.occurred_at,s.request_log_id LIMIT ?`

// Separate the current second from later seconds. SQLite can seek both index
// columns for the former; a row-value comparison can rescan a long same-second
// prefix even when its EXPLAIN plan reports a time-index range.
func readScanCandidates(ctx context.Context, tx *sql.Tx, scan ClientScan) ([]scanCandidate, error) {
	items := make([]scanCandidate, 0, ScanBatchSize)
	for step := range 2 {
		query := scanSameSecond
		args := []any{scan.afterAt, scan.afterID, scan.upper, ScanBatchSize - len(items)}
		if step == 1 {
			query = scanLaterSeconds
			args = []any{scan.afterAt, scan.To, scan.upper, ScanBatchSize - len(items)}
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item scanCandidate
			var raw, ip, quality string
			if err = rows.Scan(&item.id, &item.at, &raw, &ip, &quality, &item.kind, &item.model); err != nil {
				rows.Close()
				return nil, err
			}
			item.source, err = decodeSource(raw)
			if err != nil {
				item.source = Source{Quality: map[string]FieldQuality{"source": {Invalid: true}}}
			}
			item.source.EffectiveIP, item.source.IPQuality = ip, quality
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(items) == ScanBatchSize {
			break
		}
	}
	return items, nil
}

// RunScans owns a single resumable worker. Temporary failures are bounded;
// session credentials are never persisted in a background task.
func (r *Repository) RunScans(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		_, cleanupErr := r.CleanupScans(ctx)
		if cleanupErr != nil && ctx.Err() == nil {
			slog.Warn("client audit scan retention failed", "error_class", "storage_or_deadline")
		}
		progress, err := r.ProcessScanBatch(ctx)
		delay := time.Second
		if progress {
			delay = 250 * time.Millisecond
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("client audit scan batch failed", "error_class", "storage_or_deadline")
			delay = 5 * time.Second
		}
		timer.Reset(delay)
	}
}
