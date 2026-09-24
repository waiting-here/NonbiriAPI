package riskaudit

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"
)

const maxReadMinutes = 200000

type UserSummary struct {
	UserID                 int64  `json:"user_id,string"`
	RPMCommitted           int64  `json:"rpm_committed"`
	RPMDenied              int64  `json:"rpm_denied"`
	ConcurrencyDenied      int64  `json:"concurrency_denied"`
	Peak                   int    `json:"peak"`
	OccupancyMillis        int64  `json:"occupancy_millis"`
	CompleteMinutes        int    `json:"complete_minutes"`
	IncompleteMinutes      int    `json:"incomplete_minutes"`
	HighRPMMinutes         int    `json:"high_rpm_minutes"`
	HighConcurrencyMinutes int    `json:"high_concurrency_minutes"`
	RPMRisk                bool   `json:"rpm_risk"`
	ConcurrencyRisk        bool   `json:"concurrency_risk"`
	RiskScope              string `json:"risk_scope"`
}

// SummarizeMinutes compares only complete consecutive observations from one
// process and policy revision. Gaps and multiple epochs never become zeros.
func SummarizeMinutes(userID int64, values []Minute, gaps map[int64]bool, cfg Config, now int64, kind string) UserSummary {
	out := UserSummary{UserID: userID, RiskScope: "total"}
	if kind == "" {
		kind = "total"
	}
	counts := make(map[int64]int)
	totals := make([]Minute, 0)
	for _, m := range values {
		if m.UserID != userID {
			continue
		}
		if m.Kind == kind {
			out.RPMCommitted += m.RPMCommitted
			out.RPMDenied += m.RPMDenied
			out.ConcurrencyDenied += m.ConcurrencyDenied
			out.OccupancyMillis += m.OccupancyMillis
			out.Peak = max(out.Peak, m.Peak)
		}
		if m.Kind == "total" {
			counts[m.Minute]++
			totals = append(totals, m)
		}
	}
	sort.Slice(totals, func(i, j int) bool {
		if totals[i].Minute == totals[j].Minute {
			return totals[i].Epoch < totals[j].Epoch
		}
		return totals[i].Minute < totals[j].Minute
	})
	var previous Minute
	rpmRun, concurrencyRun := 0, 0
	for _, m := range totals {
		complete := m.Minute+60 <= now && m.UpdatedAt >= m.Minute+60 && m.Coverage == 0 && m.RPMPending == 0 && m.RPMLimit > 0 && m.ConcurrencyLimit > 0 && m.ConfigRevision == cfg.Revision && counts[m.Minute] == 1 && !gaps[m.Minute]
		if !complete {
			out.IncompleteMinutes++
			rpmRun = 0
			concurrencyRun = 0
			previous = Minute{}
			continue
		}
		out.CompleteMinutes++
		if previous.Minute+60 != m.Minute || previous.Epoch != m.Epoch || previous.ConfigRevision != m.ConfigRevision {
			rpmRun = 0
			concurrencyRun = 0
		}
		if float64(m.RPMCommitted)*100 >= float64(m.RPMLimit)*float64(cfg.ThresholdPercent) {
			rpmRun++
		} else {
			rpmRun = 0
		}
		if float64(m.OccupancyMillis)*100 >= 60000*float64(m.ConcurrencyLimit)*float64(cfg.ThresholdPercent) {
			concurrencyRun++
		} else {
			concurrencyRun = 0
		}
		out.HighRPMMinutes = max(out.HighRPMMinutes, rpmRun)
		out.HighConcurrencyMinutes = max(out.HighConcurrencyMinutes, concurrencyRun)
		previous = m
	}
	out.RPMRisk = out.HighRPMMinutes >= cfg.ConsecutiveMinutes
	out.ConcurrencyRisk = out.HighConcurrencyMinutes >= cfg.ConsecutiveMinutes
	return out
}
func readGaps(ctx context.Context, tx *sql.Tx, from, to int64) (map[int64]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT minute FROM risk_audit_gaps WHERE minute>=? AND minute<? ORDER BY minute LIMIT 43201`, from/60*60, to)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	result := make(map[int64]bool)
	for rows.Next() {
		var m int64
		if rows.Scan(&m) != nil {
			return nil, ErrUnavailable
		}
		result[m] = true
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return result, nil
}
func readMinutes(ctx context.Context, tx *sql.Tx, users []int64, w Window) ([]Minute, bool, error) {
	if len(users) == 0 {
		return []Minute{}, false, nil
	}
	if len(users) > MaxPage {
		return nil, false, ErrInvalid
	}
	args := make([]any, 0, len(users)+4)
	placeholders := make([]string, len(users))
	for i, id := range users {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, w.From, w.To)
	kind := w.Kind
	if kind == "" {
		kind = "total"
	}
	args = append(args, kind, maxReadMinutes+1)
	rows, err := tx.QueryContext(ctx, `SELECT epoch,user_id,minute,call_kind,rpm_committed,rpm_released,rpm_pending,rpm_denied,concurrency_denied,occupancy_millis,peak,COALESCE(rpm_limit,0),COALESCE(concurrency_limit,0),coverage,config_revision,updated_at FROM risk_audit_minutes WHERE user_id IN (`+strings.Join(placeholders, ",")+`) AND minute>=? AND minute<? AND call_kind IN ('total',?) ORDER BY user_id,minute,epoch,call_kind LIMIT ?`, args...)
	if err != nil {
		return nil, false, ErrUnavailable
	}
	defer rows.Close()
	result := make([]Minute, 0)
	for rows.Next() {
		var m Minute
		if rows.Scan(&m.Epoch, &m.UserID, &m.Minute, &m.Kind, &m.RPMCommitted, &m.RPMReleased, &m.RPMPending, &m.RPMDenied, &m.ConcurrencyDenied, &m.OccupancyMillis, &m.Peak, &m.RPMLimit, &m.ConcurrencyLimit, &m.Coverage, &m.ConfigRevision, &m.UpdatedAt) != nil {
			return nil, false, ErrUnavailable
		}
		result = append(result, m)
	}
	if rows.Err() != nil {
		return nil, false, ErrUnavailable
	}
	more := len(result) > maxReadMinutes
	if more {
		result = result[:maxReadMinutes]
	}
	return result, more, nil
}
func (r *Repository) Users(ctx context.Context, actor Actor, window Window) (Page[UserSummary], error) {
	w, err := window.validate(r.now().Unix())
	if err != nil {
		return Page[UserSummary]{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return Page[UserSummary]{}, err
	}
	defer tx.Rollback()
	config, err := readConfig(ctx, tx)
	if err != nil {
		return Page[UserSummary]{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM (SELECT user_id FROM risk_audit_minutes WHERE minute>=? AND minute<? AND user_id>? UNION SELECT user_id FROM request_source_facts WHERE occurred_at>=? AND occurred_at<? AND user_id>? AND kind IN ('self','charity','unclassified')) ORDER BY user_id LIMIT ?`, w.From, w.To, w.After, w.From, w.To, w.After, w.Limit+1)
	if err != nil {
		return Page[UserSummary]{}, ErrUnavailable
	}
	users := make([]int64, 0, w.Limit+1)
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			rows.Close()
			return Page[UserSummary]{}, ErrUnavailable
		}
		users = append(users, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Page[UserSummary]{}, ErrUnavailable
	}
	page := Page[UserSummary]{Items: make([]UserSummary, 0), From: w.From, To: w.To, Coverage: "observed_minutes", HasMore: len(users) > w.Limit}
	if page.HasMore {
		users = users[:w.Limit]
	}
	if len(users) > 0 {
		page.Next = users[len(users)-1]
	}
	minutes, truncated, err := readMinutes(ctx, tx, users, w)
	if err != nil {
		return page, err
	}
	if truncated {
		page.Coverage = "bounded_scan"
	}
	gaps, err := readGaps(ctx, tx, w.From, w.To)
	if err != nil {
		return page, err
	}
	for _, user := range users {
		page.Items = append(page.Items, SummarizeMinutes(user, minutes, gaps, config, w.To, w.Kind))
	}
	page.Scanned = len(users)
	return page, nil
}

type SourceRequest struct {
	LogID            int64   `json:"log_id,string"`
	UserID           int64   `json:"user_id,string"`
	RequestID        string  `json:"request_id"`
	Kind             string  `json:"call_kind"`
	OccurredAt       int64   `json:"occurred_at"`
	Source           Source  `json:"source"`
	Model            string  `json:"model"`
	Outcome          string  `json:"outcome"`
	Dispatched       bool    `json:"dispatched"`
	ErrorCode        string  `json:"error_code"`
	RejectionReason  string  `json:"rejection_reason"`
	DurationMillis   *int64  `json:"duration_millis"`
	ResponseStarted  *bool   `json:"response_started"`
	Matches          []Match `json:"matches"`
	MatchCount       int     `json:"match_count"`
	MatchesTruncated bool    `json:"matches_truncated"`
}

func sourcesTx(ctx context.Context, tx *sql.Tx, w Window, user int64) (Page[SourceRequest], error) {
	page := Page[SourceRequest]{Items: make([]SourceRequest, 0), From: w.From, To: w.To, Coverage: "source_page"}
	query := `SELECT s.request_log_id,s.user_id,l.logical_request_id,s.kind,s.occurred_at,s.source_json,s.effective_ip,s.ip_quality,l.model,COALESCE(l.caller_result_class,'running'),EXISTS(SELECT 1 FROM dispatch_claims dc WHERE dc.logical_request_id=l.logical_request_id AND dc.dispatched_at IS NOT NULL),l.error_code,COALESCE(l.rejection_reason,''),l.duration_ms,l.completed_at FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE s.user_id IS NOT NULL AND s.occurred_at>=? AND s.occurred_at<? AND s.request_log_id>? AND s.kind IN ('self','charity','unclassified')`
	args := []any{w.From, w.To, w.After}
	if user > 0 {
		query += ` AND s.user_id=?`
		args = append(args, user)
	} else if user < 0 {
		query += ` AND s.user_id<>?`
		args = append(args, -user)
	}
	if w.Model != "" {
		query += ` AND l.model=?`
		args = append(args, w.Model)
	}
	if w.Kind != "" && w.Kind != "total" {
		query += ` AND s.kind=?`
		args = append(args, w.Kind)
	}
	query += ` ORDER BY s.request_log_id LIMIT ?`
	args = append(args, w.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return page, ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var entry SourceRequest
		var raw, ip, quality string
		var attempts int
		var duration int64
		var completed sql.NullInt64
		if rows.Scan(&entry.LogID, &entry.UserID, &entry.RequestID, &entry.Kind, &entry.OccurredAt, &raw, &ip, &quality, &entry.Model, &entry.Outcome, &attempts, &entry.ErrorCode, &entry.RejectionReason, &duration, &completed) != nil {
			return page, ErrUnavailable
		}
		entry.Source, err = decodeSource(raw)
		if err != nil {
			entry.Source = Source{Quality: map[string]FieldQuality{"source": {Invalid: true}}}
			page.Coverage = "invalid_source"
		}
		entry.Source.EffectiveIP, entry.Source.IPQuality = ip, quality
		entry.Dispatched = attempts > 0
		if completed.Valid {
			entry.DurationMillis = &duration
		}
		page.Items = append(page.Items, entry)
	}
	if rows.Err() != nil {
		return page, ErrUnavailable
	}
	page.HasMore = len(page.Items) > w.Limit
	if page.HasMore {
		page.Items = page.Items[:w.Limit]
	}
	page.Scanned = len(page.Items)
	if len(page.Items) > 0 {
		page.Next = page.Items[len(page.Items)-1].LogID
	}
	return page, nil
}
func (r *Repository) ClientMatches(ctx context.Context, actor Actor, window Window) (Page[SourceRequest], error) {
	w, err := window.validate(r.now().Unix())
	if err != nil {
		return Page[SourceRequest]{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return Page[SourceRequest]{}, err
	}
	defer tx.Rollback()
	rules, err := rulesTx(ctx, tx)
	if err != nil {
		return Page[SourceRequest]{}, err
	}
	page, err := sourcesTx(ctx, tx, w, 0)
	if err != nil {
		return page, err
	}
	matched := make([]SourceRequest, 0)
	matchBudget := 100
	for _, entry := range page.Items {
		if ctx.Err() != nil {
			return page, ErrUnavailable
		}
		attachMatches(&entry, rules, &matchBudget)
		if entry.MatchCount > 0 {
			matched = append(matched, entry)
		}
	}
	page.Items = matched
	return page, nil
}

// A bounded detail budget avoids repeating large rule evidence for every call.
// The full match count remains visible when individual details are omitted.
func attachMatches(entry *SourceRequest, rules []Rule, budget *int) {
	matches := MatchRules(entry.Source, rules)
	entry.MatchCount = len(matches)
	n := min(len(matches), 20, *budget)
	entry.Matches = matches[:n]
	entry.MatchesTruncated = n < len(matches)
	*budget -= n
}

type CancellationBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}
type UserDetail struct {
	UserID                      int64                `json:"user_id,string"`
	Summary                     UserSummary          `json:"summary"`
	Minutes                     []Minute             `json:"minutes"`
	Requests                    Page[SourceRequest]  `json:"requests"`
	Cancellations               []CancellationBucket `json:"cancellations"`
	UnknownCancellationDuration int                  `json:"unknown_cancellation_duration"`
	Coverage                    string               `json:"coverage"`
	Comparison                  Comparison           `json:"comparison"`
	SourceDistribution          []SourceCount        `json:"source_distribution"`
}

type SourceCount struct {
	UserAgent string `json:"user_agent"`
	IPQuality string `json:"ip_quality"`
	Count     int    `json:"count"`
}
type SampleStats struct {
	Samples         int                  `json:"samples"`
	Dispatched      int                  `json:"dispatched"`
	Rejected        int                  `json:"rejected"`
	Failed          int                  `json:"failed"`
	UnknownDuration int                  `json:"unknown_duration"`
	Cancellations   []CancellationBucket `json:"cancellations"`
}
type Comparison struct {
	Model    string      `json:"model"`
	From     int64       `json:"from"`
	To       int64       `json:"to"`
	User     SampleStats `json:"user"`
	Others   SampleStats `json:"others"`
	HasMore  bool        `json:"has_more"`
	Coverage string      `json:"coverage"`
}

func sampleStats(values []SourceRequest, model string) SampleStats {
	out := SampleStats{Cancellations: []CancellationBucket{{Label: "[0,2)"}, {Label: "[2,30)"}, {Label: "[30,120)"}, {Label: "[120,130)"}, {Label: "[130,+∞)"}}}
	for _, v := range values {
		if v.Model != model {
			continue
		}
		out.Samples++
		if v.Dispatched {
			out.Dispatched++
		}
		if v.Outcome == "failed" {
			out.Failed++
			if !v.Dispatched {
				out.Rejected++
			}
		}
		if v.Outcome == "cancelled" {
			if v.DurationMillis == nil {
				out.UnknownDuration++
			} else {
				out.Cancellations[cancellationIndex(*v.DurationMillis)].Count++
			}
		}
	}
	return out
}

func cancellationIndex(ms int64) int {
	switch {
	case ms < 2000:
		return 0
	case ms < 30000:
		return 1
	case ms < 120000:
		return 2
	case ms < 130000:
		return 3
	default:
		return 4
	}
}
func (r *Repository) User(ctx context.Context, actor Actor, userID int64, window Window) (UserDetail, error) {
	if userID <= 0 {
		return UserDetail{}, ErrInvalid
	}
	w, err := window.validate(r.now().Unix())
	if err != nil {
		return UserDetail{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return UserDetail{}, err
	}
	defer tx.Rollback()
	var exists int
	if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=?`, userID).Scan(&exists) != nil {
		return UserDetail{}, ErrUnavailable
	}
	if exists != 1 {
		return UserDetail{}, ErrNotFound
	}
	config, err := readConfig(ctx, tx)
	if err != nil {
		return UserDetail{}, err
	}
	minutes, truncated, err := readMinutes(ctx, tx, []int64{userID}, w)
	if err != nil {
		return UserDetail{}, err
	}
	gaps, err := readGaps(ctx, tx, w.From, w.To)
	if err != nil {
		return UserDetail{}, err
	}
	requests, err := sourcesTx(ctx, tx, w, userID)
	if err != nil {
		return UserDetail{}, err
	}
	rules, err := rulesTx(ctx, tx)
	if err != nil {
		return UserDetail{}, err
	}
	out := UserDetail{UserID: userID, Summary: SummarizeMinutes(userID, minutes, gaps, config, w.To, w.Kind), Requests: requests, Minutes: minutes, Coverage: "observed_minutes", Cancellations: []CancellationBucket{{Label: "[0,2)"}, {Label: "[2,30)"}, {Label: "[30,120)"}, {Label: "[120,130)"}, {Label: "[130,+∞)"}}}
	if truncated {
		out.Coverage = "bounded_scan"
	}
	if len(out.Minutes) > 100 {
		out.Minutes = out.Minutes[len(out.Minutes)-100:]
		out.Coverage = "latest_100_observations"
	}
	matchBudget := 100
	for i := range out.Requests.Items {
		item := &out.Requests.Items[i]
		attachMatches(item, rules, &matchBudget)
		if item.Outcome == "cancelled" {
			if item.DurationMillis == nil {
				out.UnknownCancellationDuration++
			} else {
				out.Cancellations[cancellationIndex(*item.DurationMillis)].Count++
			}
		}
	}
	model := w.Model
	if model == "" && len(requests.Items) > 0 {
		model = requests.Items[0].Model
	}
	out.Comparison = Comparison{Model: model, From: w.From, To: w.To, User: sampleStats(requests.Items, model), Others: sampleStats(nil, model), Coverage: "no_model_sample"}
	if model != "" {
		comparisonWindow := w
		comparisonWindow.Model = model
		comparisonWindow.After = 0
		comparisonWindow.Limit = MaxPage
		others, e := sourcesTx(ctx, tx, comparisonWindow, -userID)
		if e != nil {
			return UserDetail{}, e
		}
		out.Comparison.Others = sampleStats(others.Items, model)
		out.Comparison.HasMore = others.HasMore
		out.Comparison.Coverage = "first_100_other_logical_calls"
	}
	distribution := make(map[string]int)
	out.SourceDistribution = make([]SourceCount, 0)
	for _, item := range requests.Items {
		key := item.Source.UserAgent + "\x00" + item.Source.IPQuality
		if index, ok := distribution[key]; ok {
			out.SourceDistribution[index].Count++
		} else {
			distribution[key] = len(out.SourceDistribution)
			out.SourceDistribution = append(out.SourceDistribution, SourceCount{UserAgent: item.Source.UserAgent, IPQuality: item.Source.IPQuality, Count: 1})
		}
	}
	return out, nil
}

type IPAssociation struct {
	UserID     int64  `json:"user_id,string"`
	Kind       string `json:"call_kind"`
	FirstSeen  int64  `json:"first_seen"`
	LastSeen   int64  `json:"last_seen"`
	Requests   int64  `json:"requests"`
	Dispatched int64  `json:"dispatched"`
	Rejected   int64  `json:"rejected"`
}
type SharedIP struct {
	IP                    string          `json:"ip"`
	Users                 int             `json:"users"`
	Requests              int64           `json:"requests"`
	FirstSeen             int64           `json:"first_seen"`
	LastSeen              int64           `json:"last_seen"`
	Associations          []IPAssociation `json:"associations"`
	AssociationsTruncated bool            `json:"associations_truncated"`
}
type SharedIPPage struct {
	Items    []SharedIP `json:"items"`
	Next     string     `json:"next,omitempty"`
	HasMore  bool       `json:"has_more"`
	From     int64      `json:"from"`
	To       int64      `json:"to"`
	Coverage string     `json:"coverage"`
}

func (r *Repository) SharedIPs(ctx context.Context, actor Actor, window Window, after string) (SharedIPPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return SharedIPPage{}, err
	}
	defer tx.Rollback()
	config, err := readConfig(ctx, tx)
	if err != nil {
		return SharedIPPage{}, err
	}
	if window.To == 0 {
		window.To = r.now().Unix()
	}
	if window.From == 0 {
		window.From = window.To - int64(config.SharedIPHours)*3600
	}
	w, err := window.validate(r.now().Unix())
	if err != nil {
		return SharedIPPage{}, err
	}
	if after != "" {
		normalized, ok := normalizeIP(after)
		if !ok || normalized != after {
			return SharedIPPage{}, ErrInvalid
		}
	}
	page := SharedIPPage{Items: make([]SharedIP, 0), From: w.From, To: w.To, Coverage: "authenticated_logical_calls"}
	rows, err := tx.QueryContext(ctx, `SELECT effective_ip,COUNT(DISTINCT user_id),COUNT(*),MIN(occurred_at),MAX(occurred_at) FROM request_source_facts WHERE occurred_at>=? AND occurred_at<? AND user_id IS NOT NULL AND kind IN ('self','charity','unclassified') AND ip_quality IN ('direct_peer','trusted_forwarded') AND effective_ip>? GROUP BY effective_ip HAVING COUNT(DISTINCT user_id)>=? ORDER BY effective_ip LIMIT ?`, w.From, w.To, after, config.SharedIPUsers, w.Limit+1)
	if err != nil {
		return page, ErrUnavailable
	}
	for rows.Next() {
		var value SharedIP
		if rows.Scan(&value.IP, &value.Users, &value.Requests, &value.FirstSeen, &value.LastSeen) != nil {
			rows.Close()
			return page, ErrUnavailable
		}
		if normalized, ok := normalizeIP(value.IP); !ok || normalized != value.IP {
			page.Coverage = "invalid_ip_excluded"
			continue
		}
		page.Items = append(page.Items, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, ErrUnavailable
	}
	page.HasMore = len(page.Items) > w.Limit
	if page.HasMore {
		page.Items = page.Items[:w.Limit]
	}
	if len(page.Items) > 0 {
		page.Next = page.Items[len(page.Items)-1].IP
	}
	for i := range page.Items {
		item := &page.Items[i]
		rows, err := tx.QueryContext(ctx, `SELECT s.user_id,s.kind,MIN(s.occurred_at),MAX(s.occurred_at),COUNT(*),SUM(CASE WHEN EXISTS(SELECT 1 FROM dispatch_claims dc WHERE dc.logical_request_id=l.logical_request_id AND dc.dispatched_at IS NOT NULL) THEN 1 ELSE 0 END),SUM(CASE WHEN NOT EXISTS(SELECT 1 FROM dispatch_claims dc WHERE dc.logical_request_id=l.logical_request_id AND dc.dispatched_at IS NOT NULL) AND l.caller_result_class='failed' THEN 1 ELSE 0 END) FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE s.effective_ip=? AND s.occurred_at>=? AND s.occurred_at<? AND s.user_id IS NOT NULL AND s.kind IN ('self','charity','unclassified') AND s.ip_quality IN ('direct_peer','trusted_forwarded') GROUP BY s.user_id,s.kind ORDER BY s.user_id,s.kind LIMIT 101`, item.IP, w.From, w.To)
		if err != nil {
			return page, ErrUnavailable
		}
		item.Associations = make([]IPAssociation, 0)
		for rows.Next() {
			var entry IPAssociation
			if rows.Scan(&entry.UserID, &entry.Kind, &entry.FirstSeen, &entry.LastSeen, &entry.Requests, &entry.Dispatched, &entry.Rejected) != nil {
				rows.Close()
				return page, ErrUnavailable
			}
			item.Associations = append(item.Associations, entry)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return page, ErrUnavailable
		}
		if len(item.Associations) > 100 {
			item.Associations = item.Associations[:100]
			item.AssociationsTruncated = true
		}
	}
	return page, nil
}
