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
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	sqlite "modernc.org/sqlite"
)

const (
	ScanLifetime        = 24 * time.Hour
	ScanBatchSize       = 500
	clientScanBatchSize = 100
	MaxScanResults      = 100000
	MaxScanCandidates   = 1000000
	MaxRetainedScans    = 200
	MaxRunningScans     = 20
	MaxActorScans       = 2
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
	ScanKind      string `json:"scan_kind,omitempty"`
	Signal        string `json:"signal,omitempty"`
}

type ClientScan struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	Reason          string `json:"reason"`
	From            int64  `json:"from"`
	To              int64  `json:"to"`
	Kind            string `json:"kind"`
	Model           string `json:"model"`
	Candidates      int64  `json:"candidates,string"`
	Scanned         int64  `json:"scanned,string"`
	Matched         int    `json:"matched"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	ExpiresAt       int64  `json:"expires_at"`
	RuleCount       int    `json:"rule_count"`
	ScanKind        string `json:"scan_kind"`
	Signal          string `json:"signal,omitempty"`
	FilterRevision  int64  `json:"filter_revision"`
	Changed         bool   `json:"changed"`
	Coverage        string `json:"coverage"`
	TruncatedReason string `json:"truncated_reason,omitempty"`
	owner           int64
	admin           bool
	upper           int64
	afterAt         int64
	afterID         int64
	rules           []Rule
	checkpoint      string
	config          Config
}

type scanCheckpoint struct {
	Signal      string    `json:"signal,omitempty"`
	Config      Config    `json:"config"`
	PendingUser int64     `json:"pending_user,omitempty"`
	AfterUser   int64     `json:"after_user,omitempty"`
	PendingIP   string    `json:"pending_ip,omitempty"`
	AfterIP     string    `json:"after_ip,omitempty"`
	SourceAt    int64     `json:"source_at,omitempty"`
	SourceID    int64     `json:"source_id,omitempty"`
	IPSummary   *SharedIP `json:"ip_summary,omitempty"`
}

const scanCommonColumns = `id,user_id,admin,state,reason,from_at,to_at,call_kind,model,upper_log_id,after_at,after_log_id,candidates,scanned,matched,created_at,updated_at,expires_at,kind,filter_revision,checkpoint_json,coverage_json,changed`
const scanColumns = scanCommonColumns + `,rules_json`
const scanMetaColumns = scanCommonColumns + `,json_array_length(rules_json)`

// Completed, cancelled, and failed scans never resume. Retain only the
// non-identity configuration in their checkpoint during the 24-hour result TTL.
const scanCheckpointWithoutIdentitySQL = `json_remove(checkpoint_json,'$.pending_user','$.after_user','$.pending_ip','$.after_ip','$.source_at','$.source_id','$.ip_summary')`

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
	var coverage string
	err := row.Scan(&out.ID, &out.owner, &out.admin, &out.State, &out.Reason, &out.From, &out.To, &out.Kind, &out.Model, &out.upper, &out.afterAt, &out.afterID, &out.Candidates, &out.Scanned, &out.Matched, &out.CreatedAt, &out.UpdatedAt, &out.ExpiresAt, &out.ScanKind, &out.FilterRevision, &out.checkpoint, &coverage, &out.Changed, ruleValue)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Coverage = "complete"
	if out.State == "queued" || out.State == "running" {
		out.Coverage = "partial"
	}
	if out.State == "limited" || out.State == "failed" || out.State == "cancelled" {
		out.Coverage, out.TruncatedReason = "partial", out.Reason
	}
	if out.Changed {
		out.Coverage = "changed"
	}
	if len(coverage) > 4096 || len(out.checkpoint) > 16384 {
		return out, ErrUnavailable
	}
	var frozen scanCheckpoint
	if json.Unmarshal([]byte(out.checkpoint), &frozen) == nil {
		out.Signal, out.config = frozen.Signal, frozen.Config
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
	if input.ScanKind == "" {
		input.ScanKind = "client_hits"
	}
	if input.ScanKind != "client_hits" && input.ScanKind != "users" && input.ScanKind != "shared_ips" ||
		(input.Signal != "" && input.Signal != "rpm" && input.Signal != "concurrency") ||
		(input.ScanKind != "users" && input.Signal != "") ||
		(input.ScanKind != "client_hits" && input.Model != "") {
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
	config, err := readConfig(ctx, tx)
	if err != nil {
		return ClientScan{}, err
	}
	w := Window{From: input.From, To: input.To, Kind: input.Kind, Model: input.Model, Limit: MaxPage}
	if input.LookbackHours > 0 {
		w.From, w.To = max(0, now-input.LookbackHours*3600), now
	} else if input.ScanKind == "shared_ips" && input.From == 0 && input.To == 0 {
		w.From, w.To = max(0, now-int64(config.SharedIPHours)*3600), now
	}
	w, err = w.validate(now)
	if err != nil {
		return ClientScan{}, err
	}
	var retained, running, unfinished int
	if err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(state IN ('queued','running')),0),COALESCE(sum(user_id=? AND state IN ('queued','running')),0) FROM risk_client_scans WHERE expires_at>?`, actor.UserID, now).Scan(&retained, &running, &unfinished); err != nil {
		return ClientScan{}, err
	}
	if retained >= MaxRetainedScans || running >= MaxRunningScans || unfinished >= MaxActorScans {
		return ClientScan{}, ErrConflict
	}
	var rules []Rule
	if input.ScanKind == "client_hits" {
		rules, err = rulesTx(ctx, tx)
		if err != nil {
			return ClientScan{}, err
		}
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
	switch input.ScanKind {
	case "client_hits":
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM request_source_facts s WHERE `+where, args...).Scan(&total)
	case "users":
		err = tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM request_source_facts WHERE occurred_at>=? AND occurred_at<? AND request_log_id<=? AND user_id IS NOT NULL AND kind IN ('self','charity','unclassified'))
 +(SELECT count(*) FROM (SELECT DISTINCT user_id FROM risk_audit_minutes WHERE minute>=? AND minute<?) m WHERE NOT EXISTS (
 SELECT 1 FROM request_source_facts s WHERE s.user_id=m.user_id AND s.occurred_at>=? AND s.occurred_at<? AND s.request_log_id<=? AND s.kind IN ('self','charity','unclassified')))
`, w.From, w.To, upper, w.From, w.To, w.From, w.To, upper).Scan(&total)
	case "shared_ips":
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM request_source_facts WHERE occurred_at>=? AND occurred_at<? AND request_log_id<=? AND user_id IS NOT NULL AND kind IN ('self','charity','unclassified') AND (?='total' OR kind=?) AND ip_quality IN ('direct_peer','trusted_forwarded')`, w.From, w.To, upper, w.Kind, w.Kind).Scan(&total)
	}
	if err != nil {
		return ClientScan{}, err
	}
	id, err := db.GenerateOpaqueID("scn_")
	if err != nil {
		return ClientScan{}, err
	}
	state := "queued"
	if total == 0 || input.ScanKind == "client_hits" && len(frozen) == 0 {
		state = "completed"
	}
	checkpoint, err := json.Marshal(scanCheckpoint{Signal: input.Signal, Config: config})
	if err != nil {
		return ClientScan{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,created_at,updated_at,expires_at,kind,filter_revision,checkpoint_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, actor.UserID, actor.Admin, input.RequestToken, string(encoded), string(raw), state, w.From, w.To, w.Kind, w.Model, upper, w.From, total, now, now, now+int64(ScanLifetime/time.Second), input.ScanKind, config.Revision, string(checkpoint))
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
	return r.recentScans(ctx, actor, 10)
}

func (r *Repository) RecentTasks(ctx context.Context, actor Actor) ([]ClientScan, error) {
	return r.recentScans(ctx, actor, MaxRetainedScans)
}

func (r *Repository) recentScans(ctx context.Context, actor Actor, limit int) ([]ClientScan, error) {
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+scanMetaColumns+` FROM risk_client_scans WHERE user_id=? AND admin=? AND expires_at>? AND reason<>'permission_changed' ORDER BY created_at DESC,id DESC LIMIT ?`, actor.UserID, actor.Admin, r.now().Unix(), limit)
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
		if _, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET state='cancelled',checkpoint_json=`+scanCheckpointWithoutIdentitySQL+`,updated_at=? WHERE id=?`, out.UpdatedAt, id); err != nil {
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
	if out.Scan.ScanKind != "client_hits" {
		return out, ErrInvalid
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM risk_scan_results WHERE scan_id=? AND published=1`, id).Scan(&total); err != nil {
		return out, err
	}
	pages := max(1, (total+int64(size)-1)/int64(size))
	page = min(page, pages)
	out.Page, out.PageSize, out.TotalItems, out.TotalPages = strconv.FormatInt(page, 10), size, strconv.FormatInt(total, 10), strconv.FormatInt(pages, 10)
	rows, err := tx.QueryContext(ctx, `SELECT request_log_id FROM risk_scan_results WHERE scan_id=? AND published=1 ORDER BY row_no LIMIT ? OFFSET ?`, id, size, (page-1)*int64(size))
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

func scanBusy(err error) bool {
	var sqliteError *sqlite.Error
	if !errors.As(err, &sqliteError) {
		return false
	}
	code := sqliteError.Code() & 0xff
	return code == 5 || code == 6 // SQLITE_BUSY or SQLITE_LOCKED.
}

// A task is claimed only in RAM for one batch. The durable checkpoint remains
// the restart authority, and concurrent workers cannot select the same task.
func (r *Repository) claimScan(ctx context.Context) (string, func(), error) {
	r.scanSelection.Lock()
	defer r.scanSelection.Unlock()
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM risk_client_scans WHERE state IN ('queued','running') AND expires_at>? ORDER BY updated_at,created_at,id LIMIT ?`, r.now().Unix(), MaxRunningScans)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return "", nil, err
		}
		if _, active := r.claimedScans[id]; active {
			continue
		}
		r.claimedScans[id] = struct{}{}
		return id, func() {
			r.scanSelection.Lock()
			delete(r.claimedScans, id)
			r.scanSelection.Unlock()
		}, nil
	}
	return "", nil, rows.Err()
}

// ProcessScanBatch commits at most one batch. A cancelled transaction leaves
// its previous checkpoint intact, including when the service is shutting down.
func (r *Repository) ProcessScanBatch(ctx context.Context) (progress bool, result error) {
	parent := ctx
	select {
	case r.scanSlots <- struct{}{}:
		defer func() { <-r.scanSlots }()
	default:
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	scanID, release, err := r.claimScan(ctx)
	if err != nil || scanID == "" {
		return false, err
	}
	defer release()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	defer func() {
		// Two independent task workers may race for SQLite's single writer.
		// A BUSY/LOCKED snapshot is a scheduling retry, not a failed task.
		if result != nil && parent.Err() == nil && scanBusy(result) {
			progress, result = false, nil
			return
		}
		if result == nil || parent.Err() != nil || scanID == "" {
			return
		}
		_ = tx.Rollback()
		bounded, cancel := context.WithTimeout(parent, 2*time.Second)
		defer cancel()
		_, _ = r.db.ExecContext(bounded, `UPDATE risk_client_scans SET failures=failures+1,state=CASE WHEN failures>=2 THEN 'failed' ELSE state END,reason=CASE WHEN failures>=2 THEN 'scan_failed' ELSE reason END,checkpoint_json=CASE WHEN failures>=2 THEN `+scanCheckpointWithoutIdentitySQL+` ELSE checkpoint_json END WHERE id=? AND state IN ('queued','running')`, scanID)
	}()
	now := r.now().Unix()
	scan, err := readScan(tx.QueryRowContext(ctx, `SELECT `+scanColumns+` FROM risk_client_scans WHERE id=? AND state IN ('queued','running') AND expires_at>?`, scanID, now))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND ((?=1 AND is_admin=1) OR (?=0 AND is_admin=0 AND COALESCE(level,auto_level)=6)) AND (is_banned=0 OR (banned_until IS NOT NULL AND banned_until<=?)))`, scan.owner, scan.admin, scan.admin, now).Scan(&allowed)
	if err != nil {
		return false, err
	}
	if !allowed {
		_, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET state='cancelled',reason='permission_changed',checkpoint_json=`+scanCheckpointWithoutIdentitySQL+`,updated_at=? WHERE id=?`, now, scan.ID)
		if err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	if scan.ScanKind != "client_hits" {
		return r.processAggregateScanBatch(ctx, tx, scan, now)
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
		if (scan.Kind == "total" || item.kind == scan.Kind) && (scan.Model == "" || item.model == scan.Model) && len(matchRules(item.source, scan.rules, false)) > 0 {
			scan.Matched++
			if _, err = tx.ExecContext(ctx, `INSERT INTO risk_scan_results(scan_id,row_no,user_id,request_log_id,result_json,published) VALUES(?,?,?,?,'{}',1)`, scan.ID, scan.Matched, item.userID, item.id); err != nil {
				return false, err
			}
			if scan.Matched == MaxScanResults {
				scan.State, scan.Reason = "limited", "result_limit"
				break
			}
		}
		if scan.Scanned >= MaxScanCandidates {
			scan.State, scan.Reason = "limited", "candidate_limit"
			break
		}
	}
	if scan.State == "running" && len(items) < clientScanBatchSize {
		scan.State = "completed"
	}
	_, err = tx.ExecContext(ctx, `UPDATE risk_client_scans SET state=?,reason=?,after_at=?,after_log_id=?,scanned=?,matched=?,updated_at=?,failures=0 WHERE id=?`, scan.State, scan.Reason, scan.afterAt, scan.afterID, scan.Scanned, scan.Matched, now, scan.ID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

type scanCandidate struct {
	id, at, userID int64
	source         Source
	kind, model    string
}

const scanCandidateSelect = `SELECT s.request_log_id,s.occurred_at,s.user_id,s.source_json,s.effective_ip,s.ip_quality,s.kind,l.model FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE s.user_id IS NOT NULL AND s.kind IN ('self','charity','unclassified')`
const scanSameSecond = scanCandidateSelect + ` AND s.occurred_at=? AND s.request_log_id>? AND s.request_log_id<=? ORDER BY s.request_log_id LIMIT ?`
const scanLaterSeconds = scanCandidateSelect + ` AND s.occurred_at>? AND s.occurred_at<? AND s.request_log_id<=? ORDER BY s.occurred_at,s.request_log_id LIMIT ?`

// Separate the current second from later seconds. SQLite can seek both index
// columns for the former; a row-value comparison can rescan a long same-second
// prefix even when its EXPLAIN plan reports a time-index range.
func readScanCandidates(ctx context.Context, tx *sql.Tx, scan ClientScan) ([]scanCandidate, error) {
	items := make([]scanCandidate, 0, clientScanBatchSize)
	for step := range 2 {
		query := scanSameSecond
		args := []any{scan.afterAt, scan.afterID, scan.upper, clientScanBatchSize - len(items)}
		if step == 1 {
			query = scanLaterSeconds
			args = []any{scan.afterAt, scan.To, scan.upper, clientScanBatchSize - len(items)}
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item scanCandidate
			var raw, ip, quality string
			if err = rows.Scan(&item.id, &item.at, &item.userID, &raw, &ip, &quality, &item.kind, &item.model); err != nil {
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
		if len(items) == clientScanBatchSize {
			break
		}
	}
	return items, nil
}

// RunScans runs two bounded task workers. SQLite may serialize their commits,
// while RAM claims keep a task's batches exclusive within this single instance.
func (r *Repository) RunScans(ctx context.Context) {
	var workers sync.WaitGroup
	workers.Add(2)
	for index := range 2 {
		go func(cleanup bool) {
			defer workers.Done()
			r.runScanWorker(ctx, cleanup)
		}(index == 0)
	}
	workers.Wait()
}

func (r *Repository) runScanWorker(ctx context.Context, cleanup bool) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if cleanup {
			_, cleanupErr := r.CleanupScans(ctx)
			if cleanupErr != nil && ctx.Err() == nil {
				slog.Warn("client audit scan retention failed", "error_class", "storage_or_deadline")
			}
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
