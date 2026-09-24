package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type modelSnapshot struct {
	id, controlID, upstreamID string
	revision                  int64
	input                     ModelInput
	metadata                  json.RawMessage
	payment                   ledger.SketchPayment
}

func (modelSnapshot) String() string { return "[private image model]" }
func modelTx(ctx context.Context, tx *sql.Tx, id string, revision int64) (modelSnapshot, error) {
	out := modelSnapshot{id: id}
	var current sql.NullInt64
	var metadata string
	err := tx.QueryRowContext(ctx, "SELECT control_id,upstream_model_id,metadata_json,current_revision FROM image_activity_models WHERE id=?", id).Scan(&out.controlID, &out.upstreamID, &metadata, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.metadata = json.RawMessage(metadata)
	out.input = ModelInput{ExpectedRevision: "0", Price: zeroPrice(), Parameters: []ParameterRule{}, Combinations: []CombinationRule{}, Mapping: normalizeMapping(Mapping{})}
	if revision == 0 {
		if !current.Valid {
			return out, nil
		}
		revision = current.Int64
	}
	var parameters, combinations, mapping string
	var paper, brush []byte
	err = tx.QueryRowContext(ctx, `SELECT revision,display_name,description,enabled,parameters_json,combinations_json,mapping_json,paper_price_mag,brush_price_mag FROM image_model_revisions WHERE model_id=? AND revision=?`, id, revision).Scan(&out.revision, &out.input.DisplayName, &out.input.Description, &out.input.Enabled, &parameters, &combinations, &mapping, &paper, &brush)
	if err != nil {
		return out, err
	}
	if json.Unmarshal([]byte(parameters), &out.input.Parameters) != nil || json.Unmarshal([]byte(combinations), &out.input.Combinations) != nil || json.Unmarshal([]byte(mapping), &out.input.Mapping) != nil {
		return out, ErrInvariant
	}
	out.payment.Paper, err = decodeAmount(paper)
	if err != nil {
		return out, err
	}
	out.payment.Brush, err = decodeAmount(brush)
	if err != nil {
		return out, err
	}
	out.input.Price = paymentPrice(out.payment)
	out.input.ExpectedRevision = strconv.FormatInt(out.revision, 10)
	if out.input.Parameters == nil {
		out.input.Parameters = []ParameterRule{}
	}
	if out.input.Combinations == nil {
		out.input.Combinations = []CombinationRule{}
	}
	out.input.Mapping = normalizeMapping(out.input.Mapping)
	return out, nil
}
func adminModelView(m modelSnapshot) AdminModel {
	return AdminModel{ID: m.id, UpstreamModelID: m.upstreamID, Metadata: m.metadata, Configured: m.revision > 0, Revision: strconv.FormatInt(m.revision, 10), DisplayName: m.input.DisplayName, Description: m.input.Description, Enabled: m.input.Enabled, Price: m.input.Price, Parameters: m.input.Parameters, Combinations: m.input.Combinations, Mapping: m.input.Mapping}
}
func publicModelView(m modelSnapshot) Model {
	return Model{ID: m.id, DisplayName: m.input.DisplayName, Description: m.input.Description, Revision: strconv.FormatInt(m.revision, 10), Price: m.input.Price, Parameters: m.input.Parameters, Combinations: m.input.Combinations}
}
func (s *Service) ListModels(ctx context.Context, user int64, admin bool, limit int, cursor string) (Page[AdminModel], error) {
	out := Page[AdminModel]{Data: []AdminModel{}}
	if !pageLimit(limit) {
		return out, ErrInvalid
	}
	now, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if admin {
		err = s.config.Admins.AuthorizeAdmin(ctx, tx, user)
	} else {
		err = s.authorizeUserTx(ctx, tx, user, now)
	}
	if err != nil {
		return out, err
	}
	upstream, err := currentUpstreamTx(ctx, tx)
	if errors.Is(err, ErrUnavailable) {
		return out, tx.Commit()
	}
	if err != nil {
		return out, err
	}
	kind := "models"
	if admin {
		kind = "admin_models"
	}
	sc := scope(kind, user, upstream.controlID)
	after, err := s.readCursor(cursor, sc)
	if err != nil {
		return out, err
	}
	query := `SELECT m.id FROM image_activity_models m LEFT JOIN image_model_revisions r ON r.model_id=m.id AND r.revision=m.current_revision WHERE m.control_id=? AND m.id>?`
	if !admin {
		query += " AND r.enabled=1"
	}
	query += " ORDER BY m.id LIMIT ?"
	rows, err := tx.QueryContext(ctx, query, upstream.controlID, after, limit+1)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(ids) > limit {
		ids = ids[:limit]
		next, e := s.cursor(sc, ids[len(ids)-1])
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	for _, id := range ids {
		m, e := modelTx(ctx, tx, id, 0)
		if e != nil {
			return out, e
		}
		out.Data = append(out.Data, adminModelView(m))
	}
	return out, tx.Commit()
}
func (s *Service) UserModels(ctx context.Context, user int64, limit int, cursor string) (Page[Model], error) {
	internal, err := s.ListModels(ctx, user, false, limit, cursor)
	out := Page[Model]{Data: []Model{}, NextCursor: internal.NextCursor}
	if err != nil {
		return out, err
	}
	for _, m := range internal.Data {
		out.Data = append(out.Data, Model{m.ID, m.DisplayName, m.Description, m.Revision, m.Price, m.Parameters, m.Combinations})
	}
	return out, nil
}
func (s *Service) PutModel(ctx context.Context, admin int64, id, key string, input ModelInput) (MutationResult[ModelReceipt], error) {
	var out MutationResult[ModelReceipt]
	if !db.ValidateOpaqueID(id, "imdl_") {
		return out, ErrInvalid
	}
	input, err := normalizeModel(input)
	if err != nil {
		return out, err
	}
	input.Mapping = normalizeMapping(input.Mapping)
	if input.Combinations == nil {
		input.Combinations = []CombinationRule{}
	}
	now, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return out, err
	}
	d, err := s.beginReplay(ctx, tx, "admin", admin, key, http.MethodPut, adminPrefix+"/models/"+id, input, now)
	if err != nil {
		return out, err
	}
	if d.Kind == idempotency.Replay {
		if json.Unmarshal(d.ResponseBody, &out.Value) != nil {
			return out, ErrInvariant
		}
		out.Replayed = true
		return out, tx.Commit()
	}
	snapshot, err := currentUpstreamTx(ctx, tx)
	if err != nil {
		return out, err
	}
	old, err := modelTx(ctx, tx, id, 0)
	if err != nil {
		return out, err
	}
	if old.controlID != snapshot.controlID {
		return out, ErrConflict
	}
	expected, _ := decimalRevision(input.ExpectedRevision, true)
	if expected != old.revision {
		return out, ErrConflict
	}
	if expected == math.MaxInt64 {
		return out, ErrCapacity
	}
	if expected == 0 {
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM image_activity_models WHERE control_id=? AND current_revision IS NOT NULL", snapshot.controlID).Scan(&count); err != nil {
			return out, err
		}
		if count >= 256 {
			return out, ErrCapacity
		}
	}
	mapping := input.Mapping
	if mapping.ModelPointer == "" {
		mapping = snapshot.adapter.Submit.Mapping
	}
	for _, rule := range input.Parameters {
		if rule.Supported {
			if _, ok := mapping.Parameters[rule.Key]; !ok {
				return out, ErrInvalid
			}
		}
	}
	parameters, _ := json.Marshal(input.Parameters)
	combinations, _ := json.Marshal(input.Combinations)
	mappingRaw, _ := json.Marshal(input.Mapping)
	payment, err := parsePayment(input.Price, 1)
	if err != nil {
		return out, err
	}
	next := expected + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO image_model_revisions(model_id,revision,display_name,description,enabled,parameters_json,combinations_json,mapping_json,paper_price_mag,brush_price_mag,actor_user_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, next, input.DisplayName, input.Description, input.Enabled, string(parameters), string(combinations), string(mappingRaw), encodeAmount(payment.Paper), encodeAmount(payment.Brush), admin, now)
	if err != nil {
		return out, err
	}
	if err = requireOne(tx.ExecContext(ctx, "UPDATE image_activity_models SET current_revision=? WHERE id=?", next, id)); err != nil {
		return out, err
	}
	var cancelled []string
	if !input.Enabled {
		cancelled, err = s.cancelQueuedTx(ctx, tx, "model_id=?", []any{id}, "model_unavailable", now)
		if err != nil {
			return out, err
		}
	}
	out.Value = ModelReceipt{ID: id, Revision: strconv.FormatInt(next, 10)}
	if err = finishReplay(ctx, tx, d, out.Value); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	for _, task := range cancelled {
		s.memory.purge(task)
	}
	s.signal()
	return out, nil
}
