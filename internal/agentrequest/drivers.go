package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/chatgptauth"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

const (
	ProviderChat      = "chat_completions"
	ProviderResponses = "responses"
	ProviderChatGPT   = "chatgpt"
)

type DriverProfile struct {
	ID       int64    `json:"id,string"`
	Name     string   `json:"name"`
	Settings Settings `json:"settings"`
}
type DriverUpdate struct {
	ExpectedRevision int64    `json:"expected_revision,string"`
	Name             string   `json:"name"`
	Settings         Settings `json:"settings"`
	APIKey           *string  `json:"api_key,omitempty"`
}
type DriverBinding struct {
	Scope    string `json:"scope"`
	ActorID  int64  `json:"actor_id,string"`
	DriverID int64  `json:"driver_id,string"`
	Revision int64  `json:"revision,string"`
}
type BindingUpdate struct {
	Scope            string `json:"scope"`
	ActorID          int64  `json:"actor_id,string"`
	DriverID         *int64 `json:"driver_id,omitempty,string"`
	ExpectedRevision int64  `json:"expected_revision,string"`
}
type DriverConfiguration struct {
	Profiles []DriverProfile `json:"profiles"`
	Bindings []DriverBinding `json:"bindings"`
}

func (r *Runtime) adminCheck(ctx context.Context) error {
	tx, err := r.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return r.adminAuthorize(ctx, tx)
}
func normalizeProvider(s *Settings) {
	if s.Provider == "" {
		s.Provider = ProviderChat
	}
	if s.OutputReserveTokens == 0 {
		s.OutputReserveTokens = s.MaxOutputTokens
	}
	if s.OutputReserveTokens == 0 {
		s.OutputReserveTokens = 4096
	}
}
func loadProfile(ctx context.Context, q settingsReader, id int64) (DriverProfile, string, error) {
	if id == 0 {
		s, key, err := loadSettings(ctx, q)
		normalizeProvider(&s)
		return DriverProfile{Name: "Default API", Settings: s}, key, err
	}
	var p DriverProfile
	var raw, key string
	var rev int64
	err := q.QueryRowContext(ctx, "SELECT id,name,revision,settings_json,encrypted_key FROM agent_driver_profiles WHERE id=?", id).Scan(&p.ID, &p.Name, &rev, &raw, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return p, "", ErrNotFound
	}
	if err != nil {
		return p, "", err
	}
	if json.Unmarshal([]byte(raw), &p.Settings) != nil {
		return p, "", ErrInvalid
	}
	p.Settings.DriverID = p.ID
	p.Settings.Revision = rev
	p.Settings.HasKey = key != ""
	normalizeProvider(&p.Settings)
	return p, key, nil
}
func (r *Runtime) resolve(ctx context.Context, q settingsReader, a Attribution) (Settings, string, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT driver_id FROM agent_driver_bindings WHERE (scope='default' AND actor_id=0) OR (scope=? AND (actor_id=0 OR (scope='steward_chat' AND actor_id=?))) ORDER BY CASE WHEN scope=? AND actor_id>0 THEN 0 WHEN scope=? THEN 1 ELSE 2 END LIMIT 1`, a.Source, a.ActorID, a.Source, a.Source).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Settings{}, "", err
	}
	p, key, err := loadProfile(ctx, q, id)
	if err != nil {
		return Settings{}, "", err
	}
	if p.Settings.Provider == ProviderChatGPT {
		if r.chatgpt == nil {
			return p.Settings, "", nil
		}
		p.Settings.HasKey, p.Settings.ConnectionRevision, err = r.chatgpt.Snapshot(ctx, q)
	}
	// The existing setting remains the process-wide scheduling limit.
	global, _, e := loadSettings(ctx, q)
	if e != nil {
		return Settings{}, "", e
	}
	p.Settings.Concurrency = global.Concurrency
	return p.Settings, key, err
}
func (r *Runtime) ResolveSettings(ctx context.Context, a Attribution) (Settings, error) {
	s, _, err := r.resolveSettingsKey(ctx, a)
	return s, err
}
func (r *Runtime) ReadyFor(ctx context.Context, tx *sql.Tx, a Attribution) (bool, error) {
	s, _, err := r.resolve(ctx, tx, a)
	return s.HasKey && s.BaseURL != "" && s.Model != "", err
}
func (r *Runtime) Drivers(ctx context.Context) (DriverConfiguration, error) {
	if err := r.adminCheck(ctx); err != nil {
		return DriverConfiguration{}, err
	}
	tx, err := r.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DriverConfiguration{}, err
	}
	defer tx.Rollback()
	legacy, _, err := loadProfile(ctx, tx, 0)
	if err != nil {
		return DriverConfiguration{}, err
	}
	out := DriverConfiguration{Profiles: []DriverProfile{legacy}, Bindings: []DriverBinding{}}
	rows, err := tx.QueryContext(ctx, "SELECT id,name,revision,settings_json,encrypted_key<>'' FROM agent_driver_profiles ORDER BY id")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p DriverProfile
		var raw string
		var rev int64
		var has bool
		if err = rows.Scan(&p.ID, &p.Name, &rev, &raw, &has); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &p.Settings); err != nil {
			break
		}
		p.Settings.DriverID = p.ID
		p.Settings.Revision = rev
		p.Settings.HasKey = has
		normalizeProvider(&p.Settings)
		out.Profiles = append(out.Profiles, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT scope,actor_id,driver_id,revision FROM agent_driver_bindings ORDER BY scope,actor_id")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b DriverBinding
		if err = rows.Scan(&b.Scope, &b.ActorID, &b.DriverID, &b.Revision); err != nil {
			return out, err
		}
		out.Bindings = append(out.Bindings, b)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if r.chatgpt != nil {
		ready, e := r.chatgpt.Ready(ctx, tx)
		if e != nil {
			return out, e
		}
		for i := range out.Profiles {
			if out.Profiles[i].Settings.Provider == ProviderChatGPT {
				out.Profiles[i].Settings.HasKey = ready
			}
		}
	}
	return out, nil
}
func (r *Runtime) validateProfile(s *Settings) error {
	normalizeProvider(s)
	s.Model = strings.TrimSpace(s.Model)
	s.BaseURL = strings.TrimSpace(s.BaseURL)
	if s.Provider != ProviderChat && s.Provider != ProviderResponses && s.Provider != ProviderChatGPT {
		return ErrInvalid
	}
	if s.ContextWindow < 1024 || s.ContextWindow > 10000000 || s.OutputReserveTokens < 1 || s.OutputReserveTokens >= s.ContextWindow || s.TimeoutSeconds < 1 || s.TimeoutSeconds > 1200 || len(s.Model) > 512 {
		return ErrInvalid
	}
	if s.Provider == ProviderChatGPT {
		if s.BaseURL != "" && s.BaseURL != chatgptauth.Resource {
			return ErrInvalid
		}
		s.BaseURL = chatgptauth.Resource
		if s.MaxOutputTokens != 0 || s.Temperature != nil || s.TopP != nil {
			return ErrInvalid
		}
	} else {
		if s.BaseURL == "" || s.MaxOutputTokens < 0 || s.MaxOutputTokens >= s.ContextWindow {
			return ErrInvalid
		}
		client, err := r.stack.NewClient(s.BaseURL)
		if err != nil {
			return ErrInvalid
		}
		s.BaseURL = client.BaseURL()
	}
	if s.Provider == ProviderChat {
		if s.OutputTokenParameter == "" {
			s.OutputTokenParameter = "max_tokens"
		}
		if s.OutputTokenParameter != "max_tokens" && s.OutputTokenParameter != "max_completion_tokens" || s.MaxOutputTokens < 1 {
			return ErrInvalid
		}
	}
	for _, pair := range []struct {
		v   *float64
		max float64
	}{{s.Temperature, 2}, {s.TopP, 1}} {
		if pair.v != nil && (math.IsNaN(*pair.v) || math.IsInf(*pair.v, 0) || *pair.v < 0 || *pair.v > pair.max) {
			return ErrInvalid
		}
	}
	if len(s.ExtraParameters) == 0 {
		s.ExtraParameters = json.RawMessage("{}")
	}
	if s.Provider == ProviderChat {
		_, err := extraParameters(s.ExtraParameters)
		return err
	}
	_, err := responsesParameters(s.ExtraParameters, s.Provider == ProviderChatGPT)
	return err
}
func (r *Runtime) PutDriver(ctx context.Context, id int64, in DriverUpdate) (DriverProfile, error) {
	if id < 0 || in.ExpectedRevision < 0 || strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 {
		return DriverProfile{}, ErrInvalid
	}
	s := in.Settings
	if err := r.validateProfile(&s); err != nil {
		return DriverProfile{}, err
	}
	s.DriverID = id
	s.ConnectionRevision = 0
	s.Revision = 0
	s.HasKey = false
	key := ""
	if in.APIKey != nil && *in.APIKey != "" {
		if s.Provider == ProviderChatGPT || len(*in.APIKey) > 65536 || strings.ContainsAny(*in.APIKey, "\r\n") {
			return DriverProfile{}, ErrInvalid
		}
		var err error
		key, err = r.secrets.SealAgentKey([]byte(*in.APIKey), "model")
		if err != nil {
			return DriverProfile{}, err
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return DriverProfile{}, err
	}
	defer tx.Rollback()
	if err = r.adminAuthorize(ctx, tx); err != nil {
		return DriverProfile{}, err
	}
	if id > 0 {
		old, previous, e := loadProfile(ctx, tx, id)
		if e != nil {
			return DriverProfile{}, e
		}
		if old.Settings.Revision != in.ExpectedRevision {
			return DriverProfile{}, ErrConflict
		}
		if in.APIKey == nil && old.Settings.Provider == s.Provider {
			key = previous
		}
	} else if in.ExpectedRevision != 0 {
		return DriverProfile{}, ErrConflict
	}
	if s.Provider == ProviderChatGPT {
		key = ""
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return DriverProfile{}, err
	}
	if key != "" {
		plain, e := r.secrets.OpenAgentKey(key, "model")
		if e != nil {
			return DriverProfile{}, e
		}
		leaked := containsSecret(raw, plain) || containsSecret([]byte(in.Name), plain)
		clear(plain)
		if leaked {
			return DriverProfile{}, ErrInvalid
		}
	}
	if id == 0 {
		result, e := tx.ExecContext(ctx, "INSERT INTO agent_driver_profiles(revision,name,settings_json,encrypted_key,updated_at) VALUES(1,?,?,?,?)", strings.TrimSpace(in.Name), string(raw), key, r.now().UnixMilli())
		if e != nil {
			return DriverProfile{}, e
		}
		id, e = result.LastInsertId()
		if e != nil {
			return DriverProfile{}, e
		}
	} else {
		if _, err = tx.ExecContext(ctx, "UPDATE agent_driver_profiles SET revision=revision+1,name=?,settings_json=?,encrypted_key=?,updated_at=? WHERE id=?", strings.TrimSpace(in.Name), string(raw), key, r.now().UnixMilli(), id); err != nil {
			return DriverProfile{}, err
		}
	}
	p, _, err := loadProfile(ctx, tx, id)
	if err != nil {
		return p, err
	}
	managementlog.SetTarget(ctx, "agent_driver", strconv.FormatInt(id, 10), p.Name)
	managementlog.ChangedFields(ctx, raw)
	if err = managementlog.Commit(ctx, tx); err == nil {
		r.signal()
	}
	return p, err
}
func (r *Runtime) DeleteDriver(ctx context.Context, id, revision int64) error {
	if id <= 0 {
		return ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = r.adminAuthorize(ctx, tx); err != nil {
		return err
	}
	var used bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agent_driver_bindings WHERE driver_id=?)", id).Scan(&used); err != nil {
		return err
	}
	if used {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM agent_driver_profiles WHERE id=? AND revision=?", id, revision)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	managementlog.SetTarget(ctx, "agent_driver", strconv.FormatInt(id, 10), "")
	return managementlog.Commit(ctx, tx)
}
func (r *Runtime) PutBinding(ctx context.Context, in BindingUpdate) (DriverBinding, error) {
	if in.Scope != "default" && in.Scope != "auto_review" && in.Scope != "admin_chat" && in.Scope != "steward_chat" || in.ActorID < 0 || in.ActorID != 0 && in.Scope != "steward_chat" || in.ExpectedRevision < 0 || in.DriverID != nil && *in.DriverID < 0 {
		return DriverBinding{}, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return DriverBinding{}, err
	}
	defer tx.Rollback()
	if err = r.adminAuthorize(ctx, tx); err != nil {
		return DriverBinding{}, err
	}
	var rev int64
	err = tx.QueryRowContext(ctx, "SELECT revision FROM agent_driver_bindings WHERE scope=? AND actor_id=?", in.Scope, in.ActorID).Scan(&rev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DriverBinding{}, err
	}
	if rev != in.ExpectedRevision {
		return DriverBinding{}, ErrConflict
	}
	out := DriverBinding{Scope: in.Scope, ActorID: in.ActorID}
	if in.DriverID == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM agent_driver_bindings WHERE scope=? AND actor_id=?", in.Scope, in.ActorID)
	} else {
		if _, _, err = loadProfile(ctx, tx, *in.DriverID); err != nil {
			return out, err
		}
		out.DriverID = *in.DriverID
		out.Revision = rev + 1
		_, err = tx.ExecContext(ctx, "INSERT INTO agent_driver_bindings(scope,actor_id,driver_id,revision) VALUES(?,?,?,?) ON CONFLICT(scope,actor_id) DO UPDATE SET driver_id=excluded.driver_id,revision=excluded.revision", in.Scope, in.ActorID, out.DriverID, out.Revision)
	}
	if err != nil {
		return out, err
	}
	managementlog.SetTarget(ctx, "agent_driver_binding", in.Scope+"/"+strconv.FormatInt(in.ActorID, 10), "")
	if in.DriverID == nil && rev == 0 {
		managementlog.SetResult(ctx, managementlog.Result{Outcome: managementlog.NoChange})
	}
	if err = managementlog.Commit(ctx, tx); err == nil {
		r.signal()
	}
	return out, err
}
