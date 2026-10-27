package authz

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

var ErrPermissionDenied = errors.New("steward_permission_denied")

func WritePermissionDenied(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, ErrPermissionDenied) {
		return false
	}
	httperr.WriteError(w, httperr.New(httperr.CodeStewardPermissionDenied, "This action requires permission from an administrator or level 6 steward."))
	return true
}

// StewardPermissions describes effective authority without changing account level.
type StewardPermissions struct {
	Revision      string            `json:"revision"`
	DonationScope DonationScope     `json:"donation_scope"`
	Permissions   map[string]bool   `json:"permissions"`
	Limits        map[string]string `json:"limits"`
}

var stewardDefaults = map[string]bool{
	"users.read": true, "users.profile": false, "users.limits": true, "users.level": false,
	"users.balance": false, "users.game_balance": false, "users.ban": true, "users.unban": false,
	"users.ban_permanent": true, "users.caller_keys.read": true, "users.caller_keys.revoke": true,
	"users.loans.read": true, "users.penalties.read": true, "users.donations.read": true,
	"blacklist.read": false, "blacklist.add": false,
	"endpoints.read": false, "endpoints.tags": false,
	"donations.review": true, "donations.read_all": false, "donations.force_reject": false,
	"donations.handle": true, "donations.manage_keys": true, "donations.discover": true, "donations.recurring": true,
	"charity.read": true, "charity.manage": true, "charity.non_mainstream": false,
	"charity.create": false, "charity.delete": false, "charity.model_settings": false, "charity.pricing": false,
	"charity.presets": false, "charity.backoff": false, "charity.price_templates.read": false,
	"logs.read": true, "logs.unredacted": false, "diagnostics.read": true, "pressure.read": true,
	"access_events.read": false, "risk.read": false, "risk.manage": false,
	"announcements.read": false, "announcements.manage": false, "maintenance.read": false,
	"maintenance.enable": false, "egress.read": false, "automation": false,
	"ai.chat": true, "ai.review.read": false, "ai.memory.edit": false,
}

var stewardLimitMaxima = map[string]int64{
	"users.endpoint_limit_max": 10000, "users.rpm_limit_max": 4096,
	"users.concurrency_limit_max":             100000,
	"users.balance_adjustment_max_milli":      9000000000000000,
	"users.game_balance_adjustment_max_milli": 9000000000000000,
	"users.ban_duration_max_seconds":          316224000,
}

type DonationScope string

const (
	DonationScopeNone     DonationScope = "none"
	DonationScopeStandard DonationScope = "standard"
	DonationScopeEnhanced DonationScope = "enhanced"
	DonationScopeFull     DonationScope = "full"
)

func (s DonationScope) Valid() bool {
	return s == DonationScopeNone || s == DonationScopeStandard || s == DonationScopeEnhanced || s == DonationScopeFull
}

func DefaultStewardPermissions(level int) StewardPermissions {
	p := StewardPermissions{Revision: "0", Permissions: make(map[string]bool, len(stewardDefaults)), Limits: make(map[string]string, len(stewardLimitMaxima))}
	p.DonationScope = DonationScopeNone
	if level == 5 {
		p.DonationScope = DonationScopeStandard
	} else if level == 6 {
		p.DonationScope = DonationScopeFull
	}
	for key, enabled := range stewardDefaults {
		p.Permissions[key] = level == 6 || level == 5 && enabled
	}
	for key, max := range stewardLimitMaxima {
		if level < 5 {
			max = 0
		}
		p.Limits[key] = strconv.FormatInt(max, 10)
	}
	return p
}

func ValidateStewardPermissions(p StewardPermissions) error {
	if !p.DonationScope.Valid() {
		return ErrInvalidRequirement
	}
	if len(p.Permissions) != len(stewardDefaults) || len(p.Limits) != len(stewardLimitMaxima) {
		return ErrInvalidRequirement
	}
	for key := range stewardDefaults {
		if _, ok := p.Permissions[key]; !ok {
			return ErrInvalidRequirement
		}
	}
	for key, max := range stewardLimitMaxima {
		raw, ok := p.Limits[key]
		n, err := strconv.ParseInt(raw, 10, 64)
		if !ok || err != nil || n < 0 || n > max || strconv.FormatInt(n, 10) != raw {
			return ErrInvalidRequirement
		}
	}
	return nil
}

// DecodeStewardPermissions is also used by the session projection's existing user query.
func DecodeStewardPermissions(level int, revision int64, permissions, limits, donationScope string) (StewardPermissions, error) {
	p := DefaultStewardPermissions(level)
	if level != 5 || revision == 0 {
		return p, nil
	}
	p.Permissions, p.Limits = nil, nil
	if revision < 0 || strictjson.ValidateObject([]byte(permissions)) != nil || strictjson.ValidateObject([]byte(limits)) != nil || json.Unmarshal([]byte(limits), &p.Limits) != nil {
		return StewardPermissions{}, ErrInvalidRequirement
	}
	var values map[string]*bool
	if json.Unmarshal([]byte(permissions), &values) != nil {
		return StewardPermissions{}, ErrInvalidRequirement
	}
	p.Permissions = make(map[string]bool, len(values))
	for key, value := range values {
		if value == nil {
			return StewardPermissions{}, ErrInvalidRequirement
		}
		p.Permissions[key] = *value
	}
	p.Revision = strconv.FormatInt(revision, 10)
	p.DonationScope = DonationScope(donationScope)
	if err := ValidateStewardPermissions(p); err != nil {
		return StewardPermissions{}, err
	}
	return p, nil
}

// ReadStewardPermissions must use the same transaction as the protected operation.
// It checks the live account, but does not replace session/CallerKey authentication.
func ReadStewardPermissions(ctx context.Context, tx *sql.Tx, userID int64) (StewardPermissions, error) {
	if ctx == nil || tx == nil || userID <= 0 {
		return StewardPermissions{}, ErrUnauthorized
	}
	var level, admin int
	var revision int64
	var permissions, limits, donationScope string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(u.level,u.auto_level),u.is_admin,COALESCE(p.revision,0),COALESCE(p.permissions,''),COALESCE(p.limits,''),COALESCE(p.donation_scope,'standard') FROM users u LEFT JOIN steward_permissions p ON p.user_id=u.id WHERE u.id=?`, userID).Scan(&level, &admin, &revision, &permissions, &limits, &donationScope)
	if errors.Is(err, sql.ErrNoRows) {
		return StewardPermissions{}, ErrUnauthorized
	}
	if err != nil {
		return StewardPermissions{}, err
	}
	if admin != 0 || level < 5 || level > 6 {
		return StewardPermissions{}, ErrForbidden
	}
	return DecodeStewardPermissions(level, revision, permissions, limits, donationScope)
}

func RequireStewardPermission(ctx context.Context, tx *sql.Tx, userID int64, key string) error {
	p, err := ReadStewardPermissions(ctx, tx, userID)
	if err != nil {
		return err
	}
	if key == "delegate" {
		var level int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level) FROM users WHERE id=?`, userID).Scan(&level); err != nil {
			return err
		}
		if level == 6 {
			return nil
		}
	}
	if key == "delegate" {
		return ErrForbidden
	}
	if !p.AllowsOperation(key) {
		return ErrPermissionDenied
	}
	return nil
}

type stewardPermissionKey struct{}

// WithStewardPermission declares an operation, never a cached authorization result.
func WithStewardPermission(ctx context.Context, permission string) context.Context {
	return context.WithValue(ctx, stewardPermissionKey{}, permission)
}

func StewardPermissionFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	p, _ := ctx.Value(stewardPermissionKey{}).(string)
	return p
}

func RequireContextStewardPermission(ctx context.Context, tx *sql.Tx, userID int64) error {
	if key := StewardPermissionFromContext(ctx); key != "" {
		return RequireStewardPermission(ctx, tx, userID, key)
	}
	return nil
}

// RequireStewardBanBounds also protects durable automatic bans after the creating
// request ends. The caller separately validates live identity and target level.
func RequireStewardBanBounds(ctx context.Context, tx *sql.Tx, actorID int64, duration *int64) error {
	p, err := ReadStewardPermissions(ctx, tx, actorID)
	if err != nil {
		return err
	}
	if !p.Permissions["users.ban"] {
		return ErrPermissionDenied
	}
	if duration == nil {
		if !p.Permissions["users.ban_permanent"] {
			return ErrPermissionDenied
		}
		return nil
	}
	max, _ := strconv.ParseInt(p.Limits["users.ban_duration_max_seconds"], 10, 64)
	if *duration <= 0 || *duration > max {
		return ErrPermissionDenied
	}
	return nil
}

// StewardRoutePermission is the shared browser/automation operation catalog.
// Domains still enforce field, target, scope and numeric checks in their transaction.
func StewardRoutePermission(method, path string) string {
	if !strings.HasPrefix(path, "/api/steward/") {
		return ""
	}
	p := strings.TrimPrefix(path, "/api/steward/")
	read := method == "GET" || method == "HEAD"
	switch {
	case (read || method == "POST" || method == "PUT") && (p == "ai/chat" || strings.HasPrefix(p, "ai/chat/")):
		return "ai.chat"
	case read && (p == "ai/reviews" || strings.HasPrefix(p, "ai/reviews/")):
		return "ai.review.read"
	case read && (p == "ai/memories/auto_review" || p == "ai/memories/chat"):
		return "ai.chat"
	case (read || method == "PUT") && p == "ai/personal-memory":
		return "ai.chat"
	case strings.HasPrefix(p, "ai/"):
		return "unconfigured"
	case strings.HasPrefix(p, "automation/"):
		inner := strings.TrimPrefix(p, "automation/")
		if strings.HasPrefix(inner, "donations") {
			if read {
				return "donations.browse"
			}
			return "donations.manage_keys"
		}
		if strings.HasPrefix(inner, "charity-models") {
			return "charity.read"
		}
		if inner == "model-bindings" {
			return "charity.manage"
		}
		if inner == "donation-key-failure-policy" {
			return "donations.manage_keys"
		}
		return "unconfigured"
	case strings.Contains(p, "steward-permissions"):
		return "delegate"
	case strings.HasPrefix(p, "users"):
		switch {
		case strings.Contains(p, "/donations"):
			return "users.donations.read"
		case strings.Contains(p, "/caller-key"):
			if read {
				return "users.caller_keys.read"
			}
			return "users.caller_keys.revoke"
		case strings.Contains(p, "/loans"):
			return "users.loans.read"
		case strings.Contains(p, "/penalties"):
			return "users.penalties.read"
		case strings.HasSuffix(p, "/unban"):
			return "users.unban"
		case strings.HasSuffix(p, "/ban"):
			return "users.ban"
		case read:
			return "users.read"
		default:
			return "user_fields"
		}
	case strings.HasPrefix(p, "overview/endpoints"):
		if read {
			return "endpoints.read"
		}
		return "endpoints.tags"
	case strings.HasPrefix(p, "blacklist"):
		if read {
			return "blacklist.read"
		}
		return "blacklist.add"
	case strings.HasPrefix(p, "abuse-audit/access-"):
		return "access_events.read"
	case strings.HasPrefix(p, "abuse-audit"):
		if read {
			return "risk.read"
		}
		return "risk.manage"
	case p == "logs/pressure":
		return "pressure.read"
	case strings.HasPrefix(p, "diagnostics"), strings.HasPrefix(p, "logs/diagnostic"), strings.Contains(p, "/headers"), strings.Contains(p, "/errors"), strings.HasSuffix(p, "/source"):
		return "diagnostics.read"
	case strings.HasPrefix(p, "logs"):
		return "logs.read"
	case strings.HasPrefix(p, "announcements"):
		if read {
			return "announcements.read"
		}
		return "announcements.manage"
	case strings.HasPrefix(p, "maintenance"):
		if read {
			return "maintenance.read"
		}
		return "maintenance.enable"
	case strings.HasPrefix(p, "egress/"):
		return "egress.read"
	case strings.Contains(p, "binding-preset"), strings.HasSuffix(p, "/bindings/apply-preset"):
		return "charity.presets"
	case strings.Contains(p, "binding-options"), strings.HasPrefix(p, "donations/") && strings.HasSuffix(p, "/bindings/batch"):
		return "charity.manage"
	case strings.Contains(p, "backoff"):
		return "charity.backoff"
	case strings.Contains(p, "price-template"):
		return "charity.price_templates.read"
	case strings.HasPrefix(p, "charity-models"):
		if strings.HasSuffix(p, "/request-adaptation") {
			return "charity.model_settings"
		}
		if !read && p == "charity-models" {
			return "charity.create"
		}
		if !read && strings.Count(p, "/") == 1 {
			if method == "DELETE" {
				return "charity.delete"
			}
			return "model_fields"
		}
		if read {
			return "charity.read"
		}
		return "charity.manage"
	case strings.HasPrefix(p, "donation-sources"):
		return "donations.manage_keys"
	case strings.HasPrefix(p, "donation-keys"):
		if strings.Contains(p, "refresh") {
			return "donations.discover"
		}
		return "donations.manage_keys"
	case strings.HasPrefix(p, "donations"):
		switch {
		case strings.Contains(p, "discover"), strings.HasSuffix(p, "/models/refresh"):
			return "donations.discover"
		case strings.Contains(p, "handled"), strings.Contains(p, "handling"), strings.Contains(p, "processed"), strings.Contains(p, "badge"):
			return "donations.handle"
		case strings.Contains(p, "recurring"):
			return "donations.recurring"
		case strings.Contains(p, "review"):
			return "donations.review"
		case strings.Contains(p, "bindings"), strings.Contains(p, "models"):
			if read {
				return "charity.read"
			}
			return "charity.manage"
		case !read:
			return "donations.manage_keys"
		default:
			return "donations.browse"
		}
	case p == "time-context":
		return "any"
	default:
		return "unconfigured"
	}
}

func (p StewardPermissions) AllowsOperation(key string) bool {
	if strings.HasPrefix(key, "donations.") && p.DonationScope == DonationScopeNone {
		return false
	}
	switch key {
	case "model_fields":
		return p.Permissions["charity.manage"] || p.Permissions["charity.model_settings"] || p.Permissions["charity.pricing"]
	case "user_fields":
		for _, k := range []string{"users.profile", "users.limits", "users.level", "users.balance", "users.game_balance"} {
			if p.Permissions[k] {
				return true
			}
		}
		return false
	case "donations.browse":
		return p.Permissions["donations.review"] || p.Permissions["donations.manage_keys"] || p.Permissions["donations.handle"] || p.Permissions["donations.discover"] || p.Permissions["donations.recurring"]
	case "any":
		for _, enabled := range p.Permissions {
			if enabled {
				return true
			}
		}
		return false
	default:
		return p.Permissions[key]
	}
}
