package imageactivity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type upstreamSnapshot struct {
	revision, stateRevision                                           int64
	controlID, baseURL, ciphertext                                    string
	secretContext                                                     []byte
	adapter                                                           Adapter
	origins                                                           []string
	userLimit, globalLimit, queueSeconds, executionSeconds, memoryMiB int
	control                                                           Control
	rpm, concurrency                                                  int
}

func (upstreamSnapshot) String() string   { return "[private image upstream]" }
func (upstreamSnapshot) GoString() string { return "[private image upstream]" }
func normalizeMapping(m Mapping) Mapping {
	if m.Parameters == nil {
		m.Parameters = map[ParameterKey]string{}
	}
	if m.Constants == nil {
		m.Constants = []Constant{}
	}
	return m
}
func normalizeAdapter(a Adapter) Adapter {
	a.Submit.Mapping = normalizeMapping(a.Submit.Mapping)
	if a.Response.WorkingStates == nil {
		a.Response.WorkingStates = []string{}
	}
	if a.Response.SuccessStates == nil {
		a.Response.SuccessStates = []string{}
	}
	if a.Response.FailureStates == nil {
		a.Response.FailureStates = []string{}
	}
	return a
}
func upstreamTx(ctx context.Context, tx *sql.Tx, revision int64) (upstreamSnapshot, error) {
	var out upstreamSnapshot
	var adapter, origins string
	var controlRevision int64
	err := tx.QueryRowContext(ctx, `SELECT r.revision,r.control_id,r.base_url,r.secret_context,r.secret_ciphertext,r.adapter_json,r.image_origins_json,r.per_user_limit,r.global_limit,r.queue_timeout_seconds,r.execution_timeout_seconds,r.memory_budget_mib,c.rpm_limit,c.concurrency_limit,c.protection_paused,c.protection_reason,c.protection_revision,(SELECT count(*) FROM image_activity_tasks t WHERE t.control_id=c.id AND t.slot_state='uncertain') FROM image_upstream_revisions r JOIN image_upstream_control c ON c.id=r.control_id WHERE r.revision=?`, revision).Scan(
		&out.revision, &out.controlID, &out.baseURL, &out.secretContext, &out.ciphertext, &adapter, &origins, &out.userLimit, &out.globalLimit, &out.queueSeconds, &out.executionSeconds, &out.memoryMiB, &out.rpm, &out.concurrency, &out.control.Paused, &out.control.Reason, &controlRevision, &out.control.UncertainSlots)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrUnavailable
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal([]byte(adapter), &out.adapter) != nil || json.Unmarshal([]byte(origins), &out.origins) != nil || ValidateAdapter(out.adapter) != nil {
		return out, ErrInvariant
	}
	out.adapter = normalizeAdapter(out.adapter)
	if out.origins == nil {
		out.origins = []string{}
	}
	out.control.ID = out.controlID
	out.control.Revision = strconv.FormatInt(controlRevision, 10)
	return out, nil
}
func currentUpstreamTx(ctx context.Context, tx *sql.Tx) (upstreamSnapshot, error) {
	var revision int64
	var target sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT revision,upstream_revision FROM image_activity_state WHERE id=1").Scan(&revision, &target); err != nil {
		return upstreamSnapshot{}, err
	}
	if !target.Valid {
		return upstreamSnapshot{stateRevision: revision}, ErrUnavailable
	}
	out, err := upstreamTx(ctx, tx, target.Int64)
	out.stateRevision = revision
	return out, err
}
func upstreamView(snapshot upstreamSnapshot) Upstream {
	a := snapshot.adapter
	c := snapshot.control
	rpm, con := snapshot.rpm, snapshot.concurrency
	return Upstream{Revision: strconv.FormatInt(snapshot.stateRevision, 10), Configured: true, BaseURL: snapshot.baseURL, SecretSet: true, RPM: &rpm, Concurrency: &con, PerUserLimit: snapshot.userLimit, GlobalLimit: snapshot.globalLimit, QueueTimeoutSeconds: snapshot.queueSeconds, ExecutionTimeoutSeconds: snapshot.executionSeconds, MemoryBudgetMiB: snapshot.memoryMiB, ImageOrigins: snapshot.origins, Adapter: &a, Control: &c}
}
func (s *Service) GetUpstream(ctx context.Context, admin int64) (Upstream, error) {
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Upstream{}, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return Upstream{}, err
	}
	snapshot, err := currentUpstreamTx(ctx, tx)
	if errors.Is(err, ErrUnavailable) {
		return Upstream{Revision: strconv.FormatInt(snapshot.stateRevision, 10), PerUserLimit: 1, GlobalLimit: 100, QueueTimeoutSeconds: 1800, ExecutionTimeoutSeconds: 1800, MemoryBudgetMiB: 512, ImageOrigins: []string{}}, tx.Commit()
	}
	if err != nil {
		return Upstream{}, err
	}
	return upstreamView(snapshot), tx.Commit()
}
func (s *Service) normalizeUpstream(input UpstreamInput) (UpstreamInput, error) {
	if _, err := decimalRevision(input.ExpectedRevision, false); err != nil {
		return input, err
	}
	if input.RPM < 1 || input.RPM > 10000 || input.Concurrency < 1 || input.Concurrency > 32 || input.PerUserLimit < 1 || input.PerUserLimit > 100 || input.GlobalLimit < 1 || input.GlobalLimit > 10000 || input.QueueTimeoutSeconds < 60 || input.QueueTimeoutSeconds > 86400 || input.ExecutionTimeoutSeconds < 60 || input.ExecutionTimeoutSeconds > 86400 || input.MemoryBudgetMiB < 512 || input.MemoryBudgetMiB > 4096 || len(input.ImageOrigins) > 8 {
		return input, ErrInvalid
	}
	if input.Secret.Mode != "keep" && input.Secret.Mode != "replace" || input.Secret.Mode == "keep" && input.Secret.Value != nil || input.Secret.Mode == "replace" && (input.Secret.Value == nil || len(*input.Secret.Value) == 0 || len(*input.Secret.Value) > secret.MaxPlaintextBytes || !safeText(*input.Secret.Value, secret.MaxPlaintextBytes)) {
		return input, ErrInvalid
	}
	u, err := url.Parse(input.BaseURL)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return input, ErrInvalid
	}
	input.BaseURL, err = s.config.Egress.ValidateBaseURL(input.BaseURL)
	if err != nil {
		return input, ErrInvalid
	}
	if err = ValidateAdapter(input.Adapter); err != nil {
		return input, err
	}
	input.Adapter = normalizeAdapter(input.Adapter)
	origins := []string{}
	seen := map[string]bool{}
	for _, raw := range input.ImageOrigins {
		u, e := url.Parse(raw)
		if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return input, ErrInvalid
		}
		canonical, e := s.config.Egress.ValidateBaseURL(raw)
		if e != nil {
			return input, ErrInvalid
		}
		_, origin, e := egress.CanonicalEndpointTarget(canonical)
		if e != nil || seen[origin] {
			return input, ErrInvalid
		}
		seen[origin] = true
		origins = append(origins, origin)
	}
	input.ImageOrigins = origins
	return input, nil
}
func (s *Service) physicalHash(base string, key []byte) ([]byte, error) {
	derived, err := s.config.Vault.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/image-upstream-identity/v1"))
	if err != nil {
		return nil, err
	}
	defer clear(derived)
	canonical, _, err := egress.CanonicalEndpointTarget(base)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, derived)
	_, _ = mac.Write([]byte(canonical))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(key)
	return mac.Sum(nil), nil
}
func (s *Service) PutUpstream(ctx context.Context, admin int64, key string, input UpstreamInput) (MutationResult[UpstreamReceipt], error) {
	var out MutationResult[UpstreamReceipt]
	input, err := s.normalizeUpstream(input)
	if err != nil {
		return out, err
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
	d, err := s.beginReplay(ctx, tx, "admin", admin, key, http.MethodPut, adminPrefix+"/upstream", input, now)
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
	previous, readErr := currentUpstreamTx(ctx, tx)
	if readErr != nil && !errors.Is(readErr, ErrUnavailable) {
		return out, readErr
	}
	expected, _ := decimalRevision(input.ExpectedRevision, false)
	if previous.stateRevision != expected {
		return out, ErrConflict
	}
	if expected == math.MaxInt64 {
		return out, ErrCapacity
	}
	var plaintext []byte
	contextID, ciphertext := previous.secretContext, previous.ciphertext
	if input.Secret.Mode == "keep" {
		if readErr != nil {
			return out, ErrInvalid
		}
		c, e := secret.NewGenerationTwoEndpointKeyContext(contextID)
		if e != nil {
			return out, ErrInvariant
		}
		plaintext, err = s.config.Vault.OpenForGenerationTwoContext(ciphertext, c)
		if err != nil {
			return out, ErrUnavailable
		}
	} else {
		plaintext = []byte(*input.Secret.Value)
		contextID, err = newContextID()
		if err != nil {
			return out, err
		}
		c, e := secret.NewGenerationTwoEndpointKeyContext(contextID)
		if e != nil {
			return out, e
		}
		ciphertext, err = s.config.Vault.SealForGenerationTwoContext(plaintext, c)
		if err != nil {
			return out, err
		}
	}
	defer clear(plaintext)
	identity, err := s.physicalHash(input.BaseURL, plaintext)
	if err != nil {
		return out, err
	}
	var controlID string
	err = tx.QueryRowContext(ctx, "SELECT id FROM image_upstream_control WHERE identity_hash=?", identity).Scan(&controlID)
	if errors.Is(err, sql.ErrNoRows) {
		controlID, err = newID("iup_")
		if err != nil {
			return out, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO image_upstream_control(id,identity_hash,rpm_limit,concurrency_limit,http_times,protection_paused,protection_reason,protection_revision,updated_at) VALUES(?,?,?,?,X'',0,'',1,?)`, controlID, identity, input.RPM, input.Concurrency, now)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, "UPDATE image_upstream_control SET rpm_limit=?,concurrency_limit=?,updated_at=? WHERE id=?", input.RPM, input.Concurrency, now, controlID)
	}
	if err != nil {
		return out, err
	}
	adapter, _ := json.Marshal(input.Adapter)
	origins, _ := json.Marshal(input.ImageOrigins)
	next := expected + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO image_upstream_revisions(revision,control_id,base_url,secret_context,secret_ciphertext,adapter_json,image_origins_json,per_user_limit,global_limit,queue_timeout_seconds,execution_timeout_seconds,memory_budget_mib,actor_user_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, next, controlID, input.BaseURL, contextID, ciphertext, string(adapter), string(origins), input.PerUserLimit, input.GlobalLimit, input.QueueTimeoutSeconds, input.ExecutionTimeoutSeconds, input.MemoryBudgetMiB, admin, now)
	if err != nil {
		return out, err
	}
	if err = requireOne(tx.ExecContext(ctx, "UPDATE image_activity_state SET revision=?,upstream_revision=?,updated_at=? WHERE id=1 AND revision=?", next, next, now, expected)); err != nil {
		return out, err
	}
	out.Value = UpstreamReceipt{Revision: strconv.FormatInt(next, 10)}
	if err = finishReplay(ctx, tx, d, out.Value); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	s.signal()
	return out, nil
}
func (s *Service) ReadyTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	if s == nil || tx == nil || s.stopped.Load() {
		return false, nil
	}
	snapshot, err := currentUpstreamTx(ctx, tx)
	if errors.Is(err, ErrUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var enabled bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM image_activity_models m JOIN image_model_revisions r ON r.model_id=m.id AND r.revision=m.current_revision WHERE m.control_id=? AND r.enabled=1)`, snapshot.controlID).Scan(&enabled)
	return enabled, err
}
func (s *Service) openSecret(snapshot upstreamSnapshot) ([]byte, error) {
	c, err := secret.NewGenerationTwoEndpointKeyContext(snapshot.secretContext)
	if err != nil {
		return nil, ErrInvariant
	}
	return s.config.Vault.OpenForGenerationTwoContext(snapshot.ciphertext, c)
}
func safeReason(value string) bool { return strings.TrimSpace(value) != "" && safeText(value, 1024) }
