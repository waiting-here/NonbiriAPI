package fatfish

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type PeriodInput struct {
	Title            string `json:"title"`
	Description      string `json:"description"`
	Visible          bool   `json:"visible"`
	Paused           bool   `json:"paused"`
	PastPublic       bool   `json:"past_public"`
	StartsAt         int64  `json:"starts_at"`
	EndsAt           int64  `json:"ends_at"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
}
type NodeInput struct {
	Title                  string          `json:"title"`
	Description            string          `json:"description"`
	MapX                   int             `json:"map_x"`
	MapY                   int             `json:"map_y"`
	Order                  int             `json:"order"`
	VersionID              string          `json:"version_id"`
	Condition              json.RawMessage `json:"condition"`
	HiddenUntilEligible    bool            `json:"hidden_until_eligible"`
	Amounts                Amounts         `json:"amounts"`
	ExpectedRevision       string          `json:"expected_revision,omitempty"`
	ExpectedPeriodRevision string          `json:"expected_period_revision"`
}
type GraphValidation struct {
	Publishable      bool     `json:"publishable"`
	Reachable        []string `json:"reachable"`
	Unreachable      []string `json:"unreachable"`
	MissingPlaytests []string `json:"missing_playtests"`
}

func (s *Service) SavePeriod(ctx context.Context, actorID int64, id string, input PeriodInput, key string) (PeriodView, error) {
	if !validText(input.Title, 128, true) || !validText(input.Description, 8192, false) || input.StartsAt < 0 || input.EndsAt <= input.StartsAt || input.EndsAt > maximumUnix {
		return PeriodView{}, ErrInvalid
	}
	if id != "" && !db.ValidateOpaqueID(id, "ffp_") {
		return PeriodView{}, ErrInvalid
	}
	var expected int64
	var err error
	if id != "" {
		expected, err = parseRevision(input.ExpectedRevision)
		if err != nil {
			return PeriodView{}, err
		}
	} else if input.ExpectedRevision != "" {
		return PeriodView{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return PeriodView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PeriodView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return PeriodView{}, err
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPut, "/periods/{id}", []string{id}, input, nowMS)
	if err != nil {
		return PeriodView{}, err
	}
	if replay, ok, replayErr := replayMutation[PeriodView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	if id == "" {
		id, err = db.GenerateOpaqueID("ffp_")
		if err != nil {
			return PeriodView{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_periods(id,title,description,state,visible,paused,past_public,starts_at,ends_at,revision,created_at,updated_at)
 VALUES(?,?,?,'draft',?,?,?,?,?,1,?,?)`, id, input.Title, input.Description, boolInt(input.Visible), boolInt(input.Paused), boolInt(input.PastPublic), input.StartsAt, input.EndsAt, nowMS/1000, nowMS/1000)
	} else {
		var oldPaused int
		if err = tx.QueryRowContext(ctx, `SELECT paused FROM fatfish_periods WHERE id=? AND revision=?`, id, expected).Scan(&oldPaused); errors.Is(err, sql.ErrNoRows) {
			return PeriodView{}, ErrConflict
		} else if err != nil {
			return PeriodView{}, err
		}
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE fatfish_periods SET title=?,description=?,visible=?,paused=?,past_public=?,starts_at=?,ends_at=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, input.Title, input.Description, boolInt(input.Visible), boolInt(input.Paused), boolInt(input.PastPublic), input.StartsAt, input.EndsAt, nowMS/1000, id, expected)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				return PeriodView{}, ErrConflict
			}
		}
		if err == nil && oldPaused == 0 && input.Paused {
			_, err = tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state='expired',terminal_at_ms=MAX(?,prepared_at_ms),terminal_reason='period_paused',revision=revision+1 WHERE period_id=? AND state='prepared'`, nowMS, id)
		}
	}
	if err != nil {
		return PeriodView{}, err
	}
	view, err := readAdminPeriodTx(ctx, tx, id)
	if err != nil {
		return PeriodView{}, err
	}
	view.Nodes = nil // Mutations return bounded metadata; GET returns the graph.
	if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
		return PeriodView{}, err
	}
	return view, tx.Commit()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func readAdminPeriodTx(ctx context.Context, tx *sql.Tx, id string) (PeriodView, error) {
	var p PeriodView
	var visible, paused, past int
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT id,title,description,state,visible,paused,past_public,starts_at,ends_at,revision FROM fatfish_periods WHERE id=?`, id).Scan(&p.ID, &p.Title, &p.Description, &p.State, &visible, &paused, &past, &p.StartsAt, &p.EndsAt, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Visible = visible == 1
	p.Paused = paused == 1
	p.PastPublic = past == 1
	p.Revision = strconv.FormatInt(revision, 10)
	p.Nodes = []NodeView{}
	rows, err := tx.QueryContext(ctx, `SELECT n.id,n.title,n.description,n.map_x,n.map_y,n.ord,n.current_revision,
	 r.version_id,r.hidden_until_eligible,r.unlock_cost_mag,r.ticket_price_mag,
	 r.first_clear_reward_mag,r.star1_reward_mag,r.star2_reward_mag,r.star3_reward_mag,v.content_hash
 FROM fatfish_nodes n JOIN fatfish_node_revisions r ON r.node_id=n.id AND r.revision=n.current_revision
 JOIN fatfish_level_versions v ON v.id=r.version_id WHERE n.period_id=? ORDER BY n.ord,n.id`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var n NodeView
		var rev int64
		var hidden int
		var mags [6][]byte
		var hash []byte
		if err = rows.Scan(&n.ID, &n.Title, &n.Description, &n.MapX, &n.MapY, &n.Order, &rev, &n.VersionID, &hidden,
			&mags[0], &mags[1], &mags[2], &mags[3], &mags[4], &mags[5], &hash); err != nil {
			return p, err
		}
		n.PeriodID = id
		n.Revision = strconv.FormatInt(rev, 10)
		n.Hidden = hidden == 1
		n.ContentHash = hex.EncodeToString(hash)
		n.Amounts = &Amounts{}
		for i, mag := range mags {
			text, err := amountText(mag)
			if err != nil {
				return p, err
			}
			switch i {
			case 0:
				n.Amounts.UnlockCost = text
			case 1:
				n.Amounts.TicketPrice = text
			case 2:
				n.Amounts.FirstClearReward = text
			case 3:
				n.Amounts.StarRewards[0] = text
			case 4:
				n.Amounts.StarRewards[1] = text
			case 5:
				n.Amounts.StarRewards[2] = text
			}
		}
		p.Nodes = append(p.Nodes, n)
	}
	return p, rows.Err()
}

func (s *Service) AdminPeriod(ctx context.Context, actorID int64, id string) (PeriodView, error) {
	if !db.ValidateOpaqueID(id, "ffp_") {
		return PeriodView{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PeriodView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return PeriodView{}, err
	}
	v, err := readAdminPeriodTx(ctx, tx, id)
	if err != nil {
		return PeriodView{}, err
	}
	return v, tx.Commit()
}

func (s *Service) AdminPeriods(ctx context.Context, actorID int64, page int) (PeriodPage, error) {
	if !validCollectionPage(page) {
		return PeriodPage{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return PeriodPage{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PeriodPage{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return PeriodPage{}, err
	}
	rows, err := tx.QueryContext(ctx, periodSummaryQuery+` ORDER BY p.starts_at DESC,p.id LIMIT ? OFFSET ?`, collectionPageSize+1, (page-1)*collectionPageSize)
	if err != nil {
		return PeriodPage{}, err
	}
	out := PeriodPage{Items: []PeriodView{}, Page: page, PageSize: collectionPageSize}
	for rows.Next() {
		item, scanErr := scanPeriodSummary(rows, nowMS)
		if scanErr != nil {
			rows.Close()
			return PeriodPage{}, scanErr
		}
		if len(out.Items) == collectionPageSize {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return PeriodPage{}, err
	}
	rows.Close()
	return out, tx.Commit()
}

func (s *Service) AdminNode(ctx context.Context, actorID int64, periodID, nodeID string) (NodeView, error) {
	if !db.ValidateOpaqueID(periodID, "ffp_") || !db.ValidateOpaqueID(nodeID, "ffn_") {
		return NodeView{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return NodeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return NodeView{}, err
	}
	p, err := readAdminPeriodTx(ctx, tx, periodID)
	if err != nil {
		return NodeView{}, err
	}
	for _, n := range p.Nodes {
		if n.ID != nodeID {
			continue
		}
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT r.condition_json FROM fatfish_nodes x JOIN fatfish_node_revisions r ON r.node_id=x.id AND r.revision=x.current_revision WHERE x.period_id=? AND x.id=?`, periodID, nodeID).Scan(&raw); err != nil {
			return NodeView{}, err
		}
		n.Condition = json.RawMessage(raw)
		return n, tx.Commit()
	}
	return NodeView{}, ErrNotFound
}

func (s *Service) SaveNode(ctx context.Context, actorID int64, periodID, nodeID string, input NodeInput, key string) (NodeView, error) {
	if !db.ValidateOpaqueID(periodID, "ffp_") || nodeID != "" && !db.ValidateOpaqueID(nodeID, "ffn_") || !db.ValidateOpaqueID(input.VersionID, "ffv_") ||
		!validText(input.Title, 128, true) || !validText(input.Description, 4096, false) || input.MapX < -1000000 || input.MapX > 1000000 || input.MapY < -1000000 || input.MapY > 1000000 || input.Order < 0 || input.Order > 127 {
		return NodeView{}, ErrInvalid
	}
	nextCondition, err := ParseCondition(input.Condition)
	if err != nil {
		return NodeView{}, err
	}
	periodRevision, err := parseRevision(input.ExpectedPeriodRevision)
	if err != nil {
		return NodeView{}, err
	}
	var nodeRevision int64
	if nodeID != "" {
		nodeRevision, err = parseRevision(input.ExpectedRevision)
		if err != nil {
			return NodeView{}, err
		}
	} else if input.ExpectedRevision != "" {
		return NodeView{}, ErrInvalid
	}
	mags := [6][]byte{}
	texts := [6]string{input.Amounts.UnlockCost, input.Amounts.TicketPrice, input.Amounts.FirstClearReward, input.Amounts.StarRewards[0], input.Amounts.StarRewards[1], input.Amounts.StarRewards[2]}
	for i, text := range texts {
		mag, err := parseAmount(text)
		if err != nil {
			return NodeView{}, err
		}
		mags[i] = db.EncodeU128(mag)
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return NodeView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return NodeView{}, err
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPut, "/periods/{p}/nodes/{n}", []string{periodID, nodeID}, input, nowMS)
	if err != nil {
		return NodeView{}, err
	}
	if replay, ok, replayErr := replayMutation[NodeView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	var state string
	var currentPeriodRevision int64
	err = tx.QueryRowContext(ctx, `SELECT state,revision FROM fatfish_periods WHERE id=?`, periodID).Scan(&state, &currentPeriodRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeView{}, ErrNotFound
	}
	if err != nil {
		return NodeView{}, err
	}
	if currentPeriodRevision != periodRevision || state == "closed" {
		return NodeView{}, ErrConflict
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM fatfish_level_versions WHERE id=?`, input.VersionID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return NodeView{}, ErrNotFound
	} else if err != nil {
		return NodeView{}, err
	}
	condition := string(input.Condition)
	if nodeID == "" {
		nodeID, err = db.GenerateOpaqueID("ffn_")
		if err != nil {
			return NodeView{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_nodes(id,period_id,title,description,map_x,map_y,ord,current_revision) VALUES(?,?,?,?,?,?,?,1)`, nodeID, periodID, input.Title, input.Description, input.MapX, input.MapY, input.Order)
		if err != nil {
			return NodeView{}, err
		}
		nodeRevision = 1
	} else {
		var current int64
		var oldVersion, oldConditionJSON string
		err = tx.QueryRowContext(ctx, `SELECT n.current_revision,r.version_id,r.condition_json FROM fatfish_nodes n JOIN fatfish_node_revisions r ON r.node_id=n.id AND r.revision=n.current_revision WHERE n.id=? AND n.period_id=?`, nodeID, periodID).Scan(&current, &oldVersion, &oldConditionJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return NodeView{}, ErrNotFound
		}
		if err != nil {
			return NodeView{}, err
		}
		if current != nodeRevision {
			return NodeView{}, ErrConflict
		}
		if state == "open" && oldConditionJSON != condition {
			oldCondition, parseErr := ParseCondition([]byte(oldConditionJSON))
			if parseErr != nil {
				return NodeView{}, ErrInvariant
			}
			if err = preserveEarnedEligibilityTx(ctx, tx, periodID, nodeID, oldCondition, nextCondition); err != nil {
				return NodeView{}, err
			}
		}
		if oldVersion != input.VersionID {
			var scored int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fatfish_progress WHERE period_id=? AND node_id=? AND passed=1`, periodID, nodeID).Scan(&scored); err != nil {
				return NodeView{}, err
			}
			if scored > 0 {
				return NodeView{}, ErrConflict
			}
		}
		nodeRevision++
		res, err := tx.ExecContext(ctx, `UPDATE fatfish_nodes SET title=?,description=?,map_x=?,map_y=?,ord=?,current_revision=? WHERE id=? AND period_id=? AND current_revision=?`, input.Title, input.Description, input.MapX, input.MapY, input.Order, nodeRevision, nodeID, periodID, current)
		if err != nil {
			return NodeView{}, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return NodeView{}, err
		}
		if affected != 1 {
			return NodeView{}, ErrConflict
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_node_revisions(node_id,revision,version_id,condition_json,hidden_until_eligible,
 unlock_cost_mag,ticket_price_mag,first_clear_reward_mag,star1_reward_mag,star2_reward_mag,star3_reward_mag,created_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, nodeID, nodeRevision, input.VersionID, condition, boolInt(input.HiddenUntilEligible), mags[0], mags[1], mags[2], mags[3], mags[4], mags[5], nowMS/1000)
	if err != nil {
		return NodeView{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE fatfish_periods SET revision=revision+1,updated_at=? WHERE id=? AND revision=?`, nowMS/1000, periodID, periodRevision)
	if err != nil {
		return NodeView{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return NodeView{}, err
	}
	if affected != 1 {
		return NodeView{}, ErrConflict
	}
	if state == "open" {
		validation, err := validateGraphTx(ctx, tx, periodID)
		if err != nil {
			return NodeView{}, err
		}
		if !validation.Publishable {
			return NodeView{}, ErrConflict
		}
	}
	p, err := readAdminPeriodTx(ctx, tx, periodID)
	if err != nil {
		return NodeView{}, err
	}
	var out NodeView
	for _, node := range p.Nodes {
		if node.ID == nodeID {
			out = node
			break
		}
	}
	if out.ID == "" {
		return NodeView{}, ErrInvariant
	}
	out.Level = nil
	if err = completeMutationTx(ctx, tx, decision, out, http.StatusOK); err != nil {
		return NodeView{}, err
	}
	return out, tx.Commit()
}

// An open graph revision cannot take away a prerequisite-qualified purchase
// opportunity from an account with formal progress. Paid unlocks remain usable
// regardless of later prerequisite edits, so they are exempt from this check.
func preserveEarnedEligibilityTx(ctx context.Context, tx *sql.Tx, periodID, targetNodeID string, before, after Condition) error {
	rows, err := tx.QueryContext(ctx, `SELECT user_id,node_id,best_stars FROM fatfish_progress WHERE period_id=? ORDER BY user_id,node_id`, periodID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var currentUser int64
	best := make(map[string]int, 128)
	unlocked := false
	hasUser := false
	check := func() error {
		if hasUser && !unlocked && before.Eligible(best) && !after.Eligible(best) {
			return ErrConflict
		}
		return nil
	}
	for rows.Next() {
		var userID int64
		var nodeID string
		var stars int
		if err := rows.Scan(&userID, &nodeID, &stars); err != nil {
			return err
		}
		if !hasUser || userID != currentUser {
			if err := check(); err != nil {
				return err
			}
			currentUser = userID
			clear(best)
			unlocked = false
			hasUser = true
		}
		best[nodeID] = stars
		unlocked = unlocked || nodeID == targetNodeID
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return check()
}

func validateGraphTx(ctx context.Context, tx *sql.Tx, periodID string) (GraphValidation, error) {
	v := GraphValidation{Reachable: []string{}, Unreachable: []string{}, MissingPlaytests: []string{}}
	rows, err := tx.QueryContext(ctx, `SELECT n.id,r.condition_json,r.version_id,lv.maximum_stars FROM fatfish_nodes n
 JOIN fatfish_node_revisions r ON r.node_id=n.id AND r.revision=n.current_revision
 JOIN fatfish_level_versions lv ON lv.id=r.version_id WHERE n.period_id=?`, periodID)
	if err != nil {
		return v, err
	}
	conditions := map[string]Condition{}
	maximum := map[string]int{}
	versions := map[string]struct{}{}
	for rows.Next() {
		var id, condition, version string
		var stars int
		if err = rows.Scan(&id, &condition, &version, &stars); err != nil {
			rows.Close()
			return v, err
		}
		parsed, err := ParseCondition([]byte(condition))
		if err != nil {
			rows.Close()
			return v, ErrInvariant
		}
		conditions[id] = parsed
		maximum[id] = stars
		versions[version] = struct{}{}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return v, err
	}
	rows.Close()
	if len(conditions) == 0 || len(conditions) > 128 {
		return v, ErrInvalid
	}
	for _, condition := range conditions {
		if err = condition.ValidateReferences(maximum); err != nil {
			return v, err
		}
	}
	reached := map[string]int{}
	for {
		changed := false
		for id, condition := range conditions {
			if reached[id] == 0 && condition.Eligible(reached) {
				reached[id] = maximum[id]
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for id := range conditions {
		if reached[id] > 0 {
			v.Reachable = append(v.Reachable, id)
		} else {
			v.Unreachable = append(v.Unreachable, id)
		}
	}
	sort.Strings(v.Reachable)
	sort.Strings(v.Unreachable)
	for version := range versions {
		var passed int
		if err = tx.QueryRowContext(ctx, `SELECT 1 FROM fatfish_playtests WHERE version_id=? AND passed=1 LIMIT 1`, version).Scan(&passed); errors.Is(err, sql.ErrNoRows) {
			v.MissingPlaytests = append(v.MissingPlaytests, version)
		} else if err != nil {
			return v, err
		}
	}
	sort.Strings(v.MissingPlaytests)
	v.Publishable = len(v.Unreachable) == 0 && len(v.MissingPlaytests) == 0
	return v, nil
}

func (s *Service) ValidatePeriod(ctx context.Context, actorID int64, id string) (GraphValidation, error) {
	if !db.ValidateOpaqueID(id, "ffp_") {
		return GraphValidation{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return GraphValidation{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return GraphValidation{}, err
	}
	v, err := validateGraphTx(ctx, tx, id)
	if err != nil {
		return GraphValidation{}, err
	}
	return v, tx.Commit()
}

func (s *Service) ChangePeriodState(ctx context.Context, actorID int64, id, action, expectedRevision, key string) (PeriodView, error) {
	if !db.ValidateOpaqueID(id, "ffp_") || action != "publish" && action != "close" && action != "reopen" {
		return PeriodView{}, ErrInvalid
	}
	expected, err := parseRevision(expectedRevision)
	if err != nil {
		return PeriodView{}, err
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return PeriodView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PeriodView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return PeriodView{}, err
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPost, "/periods/{id}/"+action, []string{id}, struct{ ExpectedRevision string }{expectedRevision}, nowMS)
	if err != nil {
		return PeriodView{}, err
	}
	if replay, ok, replayErr := replayMutation[PeriodView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	var oldState string
	var start, end, revision int64
	err = tx.QueryRowContext(ctx, `SELECT state,starts_at,ends_at,revision FROM fatfish_periods WHERE id=?`, id).Scan(&oldState, &start, &end, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return PeriodView{}, ErrNotFound
	}
	if err != nil {
		return PeriodView{}, err
	}
	if revision != expected {
		return PeriodView{}, ErrConflict
	}
	newState := "open"
	switch action {
	case "publish":
		if oldState != "draft" {
			return PeriodView{}, ErrConflict
		}
	case "reopen":
		if oldState != "closed" || nowMS >= end*1000 {
			return PeriodView{}, ErrConflict
		}
	case "close":
		if oldState != "open" {
			return PeriodView{}, ErrConflict
		}
		newState = "closed"
	}
	if newState == "open" {
		validation, err := validateGraphTx(ctx, tx, id)
		if err != nil {
			return PeriodView{}, err
		}
		if !validation.Publishable {
			return PeriodView{}, ErrConflict
		}
		var other int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fatfish_periods WHERE state='open' AND id<>?`, id).Scan(&other); err != nil {
			return PeriodView{}, err
		}
		if other != 0 {
			return PeriodView{}, ErrConflict
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE fatfish_periods SET state=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, newState, nowMS/1000, id, revision)
	if err != nil {
		return PeriodView{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return PeriodView{}, err
	}
	if affected != 1 {
		return PeriodView{}, ErrConflict
	}
	view, err := readAdminPeriodTx(ctx, tx, id)
	if err != nil {
		return PeriodView{}, err
	}
	view.Nodes = nil
	if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
		return PeriodView{}, err
	}
	return view, tx.Commit()
}
