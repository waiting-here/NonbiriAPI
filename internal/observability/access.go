package observability

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

const AccessQueueCapacity = 4096
const MaxAccessRows = 1000000

type AccessIdentity struct {
	UserID     int64
	Generation int64
}
type IdentityResolver func(context.Context, string) (AccessIdentity, error)

type AccessObserver struct {
	repository *Repository
	resolve    IdentityResolver
	queue      chan accessEvent
	mu         sync.Mutex
	closed     bool
	closingAt  atomic.Int64
	dropped    atomic.Int64
	done       chan struct{}
}

type accessEvent struct {
	identity                       AccessIdentity
	source                         Source
	pathKind, method, responseKind string
	status                         int
	at                             int64
}
type responseMarker struct {
	category atomic.Int32
	identity atomic.Pointer[AccessIdentity]
}
type responseMarkerKey struct{}

// MarkResponseCategory is called by the handler that actually selected a
// response. A successful SPA response must not look like a working API route.
func MarkResponseCategory(ctx context.Context, category string) {
	marker, _ := ctx.Value(responseMarkerKey{}).(*responseMarker)
	if marker == nil {
		return
	}
	switch category {
	case "api_json":
		marker.category.Store(1)
	case "spa_fallback":
		marker.category.Store(2)
	}
}

// MarkAccessIdentity lets an existing authenticated handler reuse its verified
// CallerKey identity without another credential lookup by the observer.
func MarkAccessIdentity(ctx context.Context, userID, generation int64) {
	marker, _ := ctx.Value(responseMarkerKey{}).(*responseMarker)
	if marker != nil && userID > 0 && generation >= 0 {
		marker.identity.Store(&AccessIdentity{UserID: userID, Generation: generation})
	}
}

func NewAccessObserver(repository *Repository, resolve IdentityResolver) (*AccessObserver, error) {
	if repository == nil || resolve == nil {
		return nil, ErrInvalid
	}
	o := &AccessObserver{repository: repository, resolve: resolve, queue: make(chan accessEvent, AccessQueueCapacity), done: make(chan struct{})}
	go o.run()
	return o, nil
}

func accessPath(method, path string) string {
	if method == http.MethodHead && path == "/v1/chat/completions" {
		return "chat_head"
	}
	if method != http.MethodGet {
		return ""
	}
	switch path {
	case "/v1/models":
		return "models"
	case "/dashboard/billing/subscription":
		return "billing_subscription"
	case "/dashboard/billing/usage":
		return "billing_usage"
	case "/v1/dashboard/billing/subscription":
		return "v1_billing_subscription"
	case "/v1/dashboard/billing/usage":
		return "v1_billing_usage"
	case "/v1/sub2api/billing":
		return "sub2api_billing"
	}
	return ""
}

func (o *AccessObserver) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := accessPath(r.Method, r.URL.Path)
		if kind == "" || httpmw.StationOf(r) != host.StationUser {
			next.ServeHTTP(w, r)
			return
		}
		event := accessEvent{pathKind: kind, method: r.Method, at: o.repository.now().Unix(), source: CaptureSource(r)}
		marker := &responseMarker{}
		ctx := context.WithValue(r.Context(), responseMarkerKey{}, marker)
		writer := &accessWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r.WithContext(ctx))
		// Authentication here is read-only: it cannot refresh sessions, consume
		// API admission capacity, or update activity. No key enters the queue.
		values := r.Header.Values("Authorization")
		if identity := marker.identity.Load(); identity != nil {
			event.identity = *identity
		} else if len(values) == 1 && len(values[0]) <= 4096 {
			parts := strings.Fields(values[0])
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				if identity, err := resolveAccessIdentity(o.resolve, r.Context(), parts[1]); err == nil && identity.UserID > 0 && identity.Generation >= 0 {
					event.identity = identity
				}
			}
		}
		if event.identity.UserID == 0 {
			event.source = Source{}
		}
		event.status = writer.status
		if event.status == 0 {
			event.status = http.StatusOK
		}
		event.responseKind = "other"
		switch marker.category.Load() {
		case 1:
			event.responseKind = "api_json"
		case 2:
			event.responseKind = "spa_fallback"
		}
		o.enqueue(event)
	})
}

func resolveAccessIdentity(resolve IdentityResolver, ctx context.Context, key string) (identity AccessIdentity, err error) {
	defer func() {
		if recover() != nil {
			identity = AccessIdentity{}
			err = ErrUnavailable
		}
	}()
	return resolve(ctx, key)
}

type accessWriter struct {
	http.ResponseWriter
	status int
}

func (w *accessWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *accessWriter) WriteHeader(status int) {
	if status >= 200 && w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *accessWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func (w *accessWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (o *AccessObserver) enqueue(event accessEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		o.dropped.Add(1)
		return
	}
	select {
	case o.queue <- event:
	default:
		o.dropped.Add(1)
	}
}

func (o *AccessObserver) run() {
	defer close(o.done)
	for event := range o.queue {
		if deadline := o.closingAt.Load(); deadline != 0 && time.Now().UnixNano() >= deadline {
			o.dropped.Add(1)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := o.repository.recordAccess(ctx, event); err != nil {
			o.dropped.Add(1)
		}
		o.flushDropped(ctx)
		cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	o.flushDropped(ctx)
}

func (o *AccessObserver) flushDropped(ctx context.Context) {
	count := o.dropped.Swap(0)
	if count == 0 {
		return
	}
	if _, err := o.repository.db.ExecContext(ctx, `UPDATE observability_state SET access_dropped=access_dropped+?,last_access_gap_at=? WHERE id=1`, count, o.repository.now().Unix()); err != nil {
		o.dropped.Add(count)
	}
}

func (o *AccessObserver) Close() error {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		o.closingAt.Store(time.Now().Add(2 * time.Second).UnixNano())
		close(o.queue)
	}
	o.mu.Unlock()
	<-o.done
	return nil
}

func (r *Repository) recordAccess(ctx context.Context, event accessEvent) error {
	if accessPath(event.method, pathForKind(event.pathKind)) == "" || event.status < 100 || event.status > 599 || event.at < 0 {
		return ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE observability_state SET access_rows=access_rows WHERE id=1`); err != nil {
		return err
	}
	valid := 0
	if event.identity.UserID > 0 {
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM caller_keys c JOIN users u ON u.id=c.user_id WHERE c.user_id=? AND c.generation=? AND c.key_hash IS NOT NULL AND u.is_admin=0 AND (u.is_banned=0 OR (u.banned_until IS NOT NULL AND u.banned_until<=?))`, event.identity.UserID, event.identity.Generation, r.now().Unix()).Scan(&valid)
		if err != nil {
			return err
		}
	}
	if valid == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO anonymous_access_minutes(minute_at,path_kind,method,status_class,count) VALUES(?,?,?,?,1) ON CONFLICT(minute_at,path_kind,method,status_class) DO UPDATE SET count=count+1`, event.at-event.at%60, event.pathKind, event.method, event.status/100)
	} else {
		var count int64
		if err = tx.QueryRowContext(ctx, `SELECT access_rows FROM observability_state WHERE id=1`).Scan(&count); err != nil {
			return err
		}
		if count >= MaxAccessRows {
			return ErrUnavailable
		}
		encoded, encodeErr := json.Marshal(event.source)
		if encodeErr != nil {
			return encodeErr
		}
		if _, err = ParseSource(encoded); err != nil {
			return err
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return err
		}
		id := "aev_" + base64.RawURLEncoding.EncodeToString(random[:])
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_access_events(id,user_id,caller_key_user_id,caller_key_generation,path_kind,method,http_status,response_kind,effective_ip,ip_quality,source_json,occurred_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, event.identity.UserID, event.identity.UserID, event.identity.Generation, event.pathKind, event.method, event.status, event.responseKind, event.source.EffectiveIP, event.source.IPQuality, string(encoded), event.at)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE observability_state SET access_rows=access_rows+1 WHERE id=1`)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func pathForKind(kind string) string {
	switch kind {
	case "models":
		return "/v1/models"
	case "billing_subscription":
		return "/dashboard/billing/subscription"
	case "billing_usage":
		return "/dashboard/billing/usage"
	case "v1_billing_subscription":
		return "/v1/dashboard/billing/subscription"
	case "v1_billing_usage":
		return "/v1/dashboard/billing/usage"
	case "sub2api_billing":
		return "/v1/sub2api/billing"
	case "chat_head":
		return "/v1/chat/completions"
	}
	return ""
}

// ClearCallerKeyTx removes historical key attribution on revocation/rotation.
// Account association remains available until ordinary deletion or retention.
func ClearCallerKeyTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE audit_access_events SET caller_key_user_id=NULL,caller_key_generation=NULL WHERE caller_key_user_id=?`, userID)
	return err
}
