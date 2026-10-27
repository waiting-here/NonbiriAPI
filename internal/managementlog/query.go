package managementlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type AdminAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}
type Service struct {
	DB, ReadDB *sql.DB
	Authorizer AdminAuthorizer
}

func New(database, readDatabase *sql.DB) *Service {
	if readDatabase == nil {
		readDatabase = database
	}
	return &Service{DB: database, ReadDB: readDatabase}
}
func nowUnix() int64 { return time.Now().UnixMilli() }

type Filter struct {
	TimeFrom     int64   `json:"time_from,omitempty"`
	TimeTo       int64   `json:"time_to,omitempty"`
	Source       Source  `json:"source,omitempty"`
	ActorID      string  `json:"actor_id,omitempty"`
	AuthorizerID string  `json:"authorizer_id,omitempty"`
	Domain       string  `json:"domain,omitempty"`
	Action       string  `json:"action,omitempty"`
	Outcome      Outcome `json:"outcome,omitempty"`
	EventID      string  `json:"event_id,omitempty"`
	TargetKind   string  `json:"target_kind,omitempty"`
	TargetID     string  `json:"target_id,omitempty"`
	GroupKey     string  `json:"group_key,omitempty"`
	Search       string  `json:"search,omitempty"`
}
type Query struct {
	Filter
	View          string `json:"view,omitempty"`
	Page          int    `json:"page"`
	PageSize      int    `json:"page_size"`
	SnapshotMaxID int64  `json:"snapshot_max_id,string"`
}
type Group struct {
	Key        string  `json:"key"`
	Bucket     int64   `json:"bucket"`
	Source     Source  `json:"source"`
	Domain     string  `json:"domain"`
	Action     string  `json:"action"`
	Actor      Subject `json:"actor"`
	Authorizer Subject `json:"authorizer"`
	Outcome    Outcome `json:"outcome"`
	Operations int64   `json:"operations,string"`
	Targets    int64   `json:"targets,string"`
	Succeeded  int64   `json:"succeeded,string"`
	Skipped    int64   `json:"skipped,string"`
	Failed     int64   `json:"failed,string"`
}
type Page struct {
	View          string      `json:"view"`
	Operations    []Operation `json:"operations"`
	Groups        []Group     `json:"groups"`
	Total         int64       `json:"total,string"`
	Page          int         `json:"page"`
	PageSize      int         `json:"page_size"`
	SnapshotMaxID int64       `json:"snapshot_max_id,string"`
}
type ItemPage struct {
	Items    []Item `json:"items"`
	Total    int64  `json:"total,string"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

func normalizeQuery(q Query) (Query, error) {
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 25
	}
	if q.Page < 1 || q.Page > 10000000 || q.PageSize < 1 || q.PageSize > 100 || q.SnapshotMaxID < 0 {
		return q, ErrInvalid
	}
	if q.View == "" {
		q.View = "groups"
	}
	if q.View != "groups" && q.View != "operations" {
		return q, ErrInvalid
	}
	if err := validateFilter(q.Filter); err != nil {
		return q, err
	}
	if q.EventID != "" || q.TargetKind != "" || q.TargetID != "" || q.GroupKey != "" || q.Search != "" {
		q.View = "operations"
	}
	return q, nil
}
func validateFilter(f Filter) error {
	if f.TimeFrom < 0 || f.TimeTo < 0 || f.TimeTo > 253402300799999 || f.TimeTo != 0 && f.TimeFrom >= f.TimeTo || f.Source != "" && !validSource(f.Source) || f.Outcome != "" && !validOutcome(f.Outcome) {
		return ErrInvalid
	}
	for _, v := range []string{f.ActorID, f.AuthorizerID, f.TargetID, f.Search} {
		if !textOK(v, 192) {
			return ErrInvalid
		}
	}
	for _, v := range []string{f.Domain, f.Action, f.TargetKind, f.EventID, f.GroupKey} {
		if !tokenOK(v, 160) {
			return ErrInvalid
		}
	}
	if f.TargetID != "" && f.TargetKind == "" {
		return ErrInvalid
	}
	return nil
}
func filterSQL(f Filter, alias string, groups bool) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}
	add := func(column string, value any) {
		clauses = append(clauses, alias+"."+column+"=?")
		args = append(args, value)
	}
	for _, v := range []struct{ column, value string }{{"source", string(f.Source)}, {"actor_id", f.ActorID}, {"authorizer_id", f.AuthorizerID}, {"domain", f.Domain}, {"action", f.Action}, {"outcome", string(f.Outcome)}, {"group_key", f.GroupKey}} {
		if v.value != "" {
			add(v.column, v.value)
		}
	}
	timeColumn := "created_at"
	if groups {
		timeColumn = "bucket"
	}
	if f.TimeFrom > 0 {
		value := f.TimeFrom
		if groups {
			value = value / 3600000 * 3600000
		}
		clauses = append(clauses, alias+"."+timeColumn+">=?")
		args = append(args, value)
	}
	if f.TimeTo > 0 {
		clauses = append(clauses, alias+"."+timeColumn+"<?")
		args = append(args, f.TimeTo)
	}
	if !groups {
		if f.EventID != "" {
			add("event_id", f.EventID)
		}
		if f.Search != "" {
			id, _ := strconv.ParseInt(f.Search, 10, 64)
			clauses = append(clauses, "("+alias+".event_id=? OR "+alias+".actor_name=? OR "+alias+".id=?)")
			args = append(args, f.Search, f.Search, id)
		}
		if f.TargetKind != "" {
			sub := alias + ".id IN (SELECT i.operation_id FROM management_operation_items i WHERE i.target_kind=?"
			args = append(args, f.TargetKind)
			if f.TargetID != "" {
				sub += " AND i.target_id=?"
				args = append(args, f.TargetID)
			}
			clauses = append(clauses, sub+")")
		}
	}
	return strings.Join(clauses, " AND "), args
}

const operationColumns = "id,event_id,created_at,source,executor_kind,actor_id,actor_name,actor_role,authorizer_id,authorizer_name,authorizer_role,authorization_ref,domain,action,correlation_kind,correlation_id,outcome,code,summary,targets,succeeded,skipped,failed,cleaning"

type scanner interface{ Scan(...any) error }

func scanOperation(row scanner) (Operation, error) {
	var v Operation
	err := row.Scan(&v.ID, &v.EventID, &v.CreatedAt, &v.Source, &v.ExecutorKind, &v.Actor.ID, &v.Actor.Name, &v.Actor.Role, &v.Authorizer.ID, &v.Authorizer.Name, &v.Authorizer.Role, &v.AuthorizationRef, &v.Domain, &v.Action, &v.CorrelationKind, &v.CorrelationID, &v.Outcome, &v.Code, &v.Summary, &v.Targets, &v.Succeeded, &v.Skipped, &v.Failed, &v.Cleaning)
	return v, err
}
func (s *Service) Query(ctx context.Context, adminID int64, q Query) (result Page, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	defer func() {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			resultErr = ErrBudget
		}
	}()
	q, err := normalizeQuery(q)
	if err != nil {
		return Page{}, err
	}
	tx, err := s.ReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback()
	if err = requireAdmin(ctx, tx, adminID); err != nil {
		return Page{}, err
	}
	if q.SnapshotMaxID == 0 {
		if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM management_operations").Scan(&q.SnapshotMaxID); err != nil {
			return Page{}, err
		}
	}
	page := Page{View: q.View, Operations: []Operation{}, Groups: []Group{}, Page: q.Page, PageSize: q.PageSize, SnapshotMaxID: q.SnapshotMaxID}
	if q.View == "groups" {
		return queryGroups(ctx, tx, q, page)
	}
	where, args := filterSQL(q.Filter, "o", false)
	where += " AND o.id<=?"
	args = append(args, q.SnapshotMaxID)
	if q.EventID == "" && q.TargetKind == "" && q.GroupKey == "" && q.Search == "" {
		rollupWhere, rollupArgs := filterSQL(q.Filter, "r", true)
		query := `SELECT COALESCE(SUM(CASE WHEN r.max_id<=? AND r.bucket>=? AND (?=0 OR r.bucket+3600000<=?) THEN r.operations ELSE
 (SELECT COUNT(*) FROM management_operations o WHERE o.group_key=r.group_key AND o.id<=? AND o.created_at>=? AND (?=0 OR o.created_at<?)) END),0)
 FROM management_operation_rollups r WHERE ` + rollupWhere + " AND r.min_id<=?"
		countArgs := []any{q.SnapshotMaxID, q.TimeFrom, q.TimeTo, q.TimeTo, q.SnapshotMaxID, q.TimeFrom, q.TimeTo, q.TimeTo}
		countArgs = append(countArgs, rollupArgs...)
		countArgs = append(countArgs, q.SnapshotMaxID)
		err = tx.QueryRowContext(ctx, query, countArgs...).Scan(&page.Total)
	} else {
		err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM management_operations o WHERE "+where, args...).Scan(&page.Total)
	}
	if err != nil {
		return page, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+operationColumns+" FROM management_operations o WHERE "+where+" ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", append(args, q.PageSize, (q.Page-1)*q.PageSize)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanOperation(rows)
		if err != nil {
			return page, err
		}
		page.Operations = append(page.Operations, v)
	}
	return page, rows.Err()
}
func queryGroups(ctx context.Context, tx *sql.Tx, q Query, page Page) (Page, error) {
	where, args := filterSQL(q.Filter, "r", true)
	where += " AND r.min_id<=? AND (r.max_id<=? AND r.bucket>=? AND (?=0 OR r.bucket+3600000<=?) OR EXISTS(SELECT 1 FROM management_operations o WHERE o.group_key=r.group_key AND o.id<=? AND o.created_at>=? AND (?=0 OR o.created_at<?)))"
	args = append(args, q.SnapshotMaxID, q.SnapshotMaxID, q.TimeFrom, q.TimeTo, q.TimeTo, q.SnapshotMaxID, q.TimeFrom, q.TimeTo, q.TimeTo)
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM management_operation_rollups r WHERE "+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT group_key,bucket,source,domain,action,actor_id,actor_name,actor_role,authorizer_id,authorizer_name,authorizer_role,outcome,operations,targets,succeeded,skipped,failed,max_id FROM management_operation_rollups r WHERE `+where+" ORDER BY bucket DESC,group_key LIMIT ? OFFSET ?", append(args, q.PageSize, (q.Page-1)*q.PageSize)...)
	if err != nil {
		return page, err
	}
	var partial []bool
	for rows.Next() {
		var g Group
		var maxID int64
		if err = rows.Scan(&g.Key, &g.Bucket, &g.Source, &g.Domain, &g.Action, &g.Actor.ID, &g.Actor.Name, &g.Actor.Role, &g.Authorizer.ID, &g.Authorizer.Name, &g.Authorizer.Role, &g.Outcome, &g.Operations, &g.Targets, &g.Succeeded, &g.Skipped, &g.Failed, &maxID); err != nil {
			rows.Close()
			return page, err
		}
		partial = append(partial, maxID > q.SnapshotMaxID || g.Bucket < q.TimeFrom || q.TimeTo > 0 && g.Bucket+3600000 > q.TimeTo)
		page.Groups = append(page.Groups, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	for i, needed := range partial {
		if needed {
			g := &page.Groups[i]
			err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(targets),0),COALESCE(SUM(succeeded),0),COALESCE(SUM(skipped),0),COALESCE(SUM(failed),0) FROM management_operations WHERE group_key=? AND id<=? AND created_at>=? AND (?=0 OR created_at<?)`, g.Key, q.SnapshotMaxID, q.TimeFrom, q.TimeTo, q.TimeTo).Scan(&g.Operations, &g.Targets, &g.Succeeded, &g.Skipped, &g.Failed)
			if err != nil {
				return page, err
			}
		}
	}
	return page, nil
}
func (s *Service) Detail(ctx context.Context, adminID, id int64) (Operation, error) {
	tx, err := s.ReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Operation{}, err
	}
	defer tx.Rollback()
	if err = requireAdmin(ctx, tx, adminID); err != nil {
		return Operation{}, err
	}
	v, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM management_operations WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}
func (s *Service) Items(ctx context.Context, adminID, id int64, page, size int) (ItemPage, error) {
	q, err := normalizeQuery(Query{Page: page, PageSize: size})
	if err != nil {
		return ItemPage{}, err
	}
	result := ItemPage{Items: []Item{}, Page: q.Page, PageSize: q.PageSize}
	tx, err := s.ReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = requireAdmin(ctx, tx, adminID); err != nil {
		return result, err
	}
	var cleaning bool
	err = tx.QueryRowContext(ctx, "SELECT targets,cleaning FROM management_operations WHERE id=?", id).Scan(&result.Total, &cleaning)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if cleaning {
		return result, ErrCleaning
	}
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,action,target_kind,target_id,target_name,outcome,code,changes FROM management_operation_items WHERE operation_id=? AND ordinal>? ORDER BY ordinal LIMIT ?`, id, (q.Page-1)*q.PageSize, q.PageSize)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Item
		var encoded string
		if err = rows.Scan(&v.Ordinal, &v.Action, &v.TargetKind, &v.TargetID, &v.TargetName, &v.Outcome, &v.Code, &encoded); err != nil {
			return result, err
		}
		if err = json.Unmarshal([]byte(encoded), &v.Changes); err != nil {
			return result, fmt.Errorf("read management changes: %w", err)
		}
		result.Items = append(result.Items, v)
	}
	return result, rows.Err()
}
