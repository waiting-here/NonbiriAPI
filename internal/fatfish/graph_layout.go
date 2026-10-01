package fatfish

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type GraphPosition struct {
	NodeID string `json:"node_id"`
	MapX   int    `json:"map_x"`
	MapY   int    `json:"map_y"`
}
type GraphLayout struct {
	Revision string          `json:"revision"`
	Nodes    []GraphPosition `json:"nodes"`
}
type GraphLayoutInput struct {
	ExpectedRevision string          `json:"expected_revision"`
	Nodes            []GraphPosition `json:"nodes"`
}
type storedGraphLayout struct {
	Nodes []GraphPosition `json:"nodes"`
}

// readGraphLayoutTx overlays saved positions onto the current node collection.
// Legacy coordinates supply defaults for nodes added after the last layout save.
func readGraphLayoutTx(ctx context.Context, tx *sql.Tx, id string) (GraphLayout, error) {
	out := GraphLayout{Revision: "0", Nodes: []GraphPosition{}}
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM fatfish_periods WHERE id=?", id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	var raw string
	var revision int64
	positions := map[string]GraphPosition{}
	err := tx.QueryRowContext(ctx, "SELECT revision,layout_json FROM fatfish_graph_layouts WHERE period_id=?", id).Scan(&revision, &raw)
	if err == nil {
		var stored storedGraphLayout
		if json.Unmarshal([]byte(raw), &stored) != nil || len(stored.Nodes) > 128 {
			return out, ErrInvariant
		}
		for _, position := range stored.Nodes {
			positions[position.NodeID] = position
		}
		out.Revision = strconv.FormatInt(revision, 10)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,map_x,map_y FROM fatfish_nodes WHERE period_id=? ORDER BY id", id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var position GraphPosition
		if err = rows.Scan(&position.NodeID, &position.MapX, &position.MapY); err != nil {
			return out, err
		}
		if saved, ok := positions[position.NodeID]; ok {
			position = saved
		}
		out.Nodes = append(out.Nodes, position)
	}
	return out, rows.Err()
}
func (s *Service) GraphLayout(ctx context.Context, actorID int64, id string) (GraphLayout, error) {
	if !db.ValidateOpaqueID(id, "ffp_") {
		return GraphLayout{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return GraphLayout{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return GraphLayout{}, err
	}
	out, err := readGraphLayoutTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) SaveGraphLayout(ctx context.Context, actorID int64, id string, input GraphLayoutInput, key string) (GraphLayout, error) {
	if !db.ValidateOpaqueID(id, "ffp_") || input.Nodes == nil || len(input.Nodes) > 128 {
		return GraphLayout{}, ErrInvalid
	}
	expected := int64(0)
	var err error
	if input.ExpectedRevision != "0" {
		expected, err = parseRevision(input.ExpectedRevision)
		if err != nil {
			return GraphLayout{}, err
		}
	}
	seen := make(map[string]bool, len(input.Nodes))
	for _, p := range input.Nodes {
		if !db.ValidateOpaqueID(p.NodeID, "ffn_") || seen[p.NodeID] || p.MapX < -1000000 || p.MapX > 1000000 || p.MapY < -1000000 || p.MapY > 1000000 {
			return GraphLayout{}, ErrInvalid
		}
		seen[p.NodeID] = true
	}
	sorted := append([]GraphPosition{}, input.Nodes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].NodeID < sorted[j].NodeID })
	raw, err := json.Marshal(storedGraphLayout{Nodes: sorted})
	if err != nil || len(raw) > 32768 {
		return GraphLayout{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return GraphLayout{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GraphLayout{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return GraphLayout{}, err
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPut, "/periods/{id}/layout", []string{id}, input, nowMS)
	if err != nil {
		return GraphLayout{}, err
	}
	if out, ok, replayErr := replayMutation[GraphLayout](decision); ok || replayErr != nil {
		return out, replayErr
	}
	current, err := readGraphLayoutTx(ctx, tx, id)
	if err != nil {
		return GraphLayout{}, err
	}
	if current.Revision != input.ExpectedRevision || len(current.Nodes) != len(input.Nodes) {
		return GraphLayout{}, ErrConflict
	}
	for _, p := range current.Nodes {
		if !seen[p.NodeID] {
			return GraphLayout{}, ErrConflict
		}
	}
	if expected == 0 {
		_, err = tx.ExecContext(ctx, "INSERT INTO fatfish_graph_layouts(period_id,revision,layout_json,updated_at) VALUES(?,1,?,?)", id, string(raw), nowMS/1000)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, "UPDATE fatfish_graph_layouts SET revision=revision+1,layout_json=?,updated_at=? WHERE period_id=? AND revision=?", string(raw), nowMS/1000, id, expected)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				return GraphLayout{}, ErrConflict
			}
		}
	}
	if err != nil {
		return GraphLayout{}, err
	}
	out := GraphLayout{Revision: strconv.FormatInt(expected+1, 10), Nodes: sorted}
	if err = completeMutationTx(ctx, tx, decision, out, http.StatusOK); err != nil {
		return GraphLayout{}, err
	}
	return out, tx.Commit()
}
