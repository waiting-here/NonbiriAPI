package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const maxSiteConfigBatchKeys = 64

type SiteConfigBatchInput struct {
	AdminID          int64
	ExpectedRevision string
	Values           map[string]json.RawMessage
	IdempotencyKey   string
}

type SiteConfigBatchResponse struct {
	Revision    string   `json:"revision"`
	ChangedKeys []string `json:"changed_keys"`
}

type siteConfigValidationError struct{ message string }

func (err *siteConfigValidationError) Error() string { return err.message }
func (err *siteConfigValidationError) Unwrap() error { return ErrSiteConfigConflict }

func siteConfigCombinationError(err error) error {
	messages := map[string]string{
		"donation intake requires charity":                     "Enable shared models before accepting donations.",
		"checkin award bounds are inverted":                    "The minimum check-in reward must not exceed the maximum.",
		"enabled level thresholds are not strictly increasing": "Enabled level thresholds must increase from level 2 to level 4.",
		"checkin requires site timezone":                       "Set the site timezone before enabling check-in.",
		"activities require site timezone":                     "Set the site timezone before enabling activities.",
	}
	if message, ok := messages[err.Error()]; ok {
		return &siteConfigValidationError{message: message}
	}
	return ErrSiteConfigConflict
}

// PatchSiteConfigBatch validates the combined configuration before changing
// any field. One transaction and replay receipt cover the entire edit.
func (repository *SiteConfigRepository) PatchSiteConfigBatch(ctx context.Context, input SiteConfigBatchInput) (SiteConfigMutationResult, error) {
	if repository == nil || ctx == nil || len(input.Values) == 0 || len(input.Values) > maxSiteConfigBatchKeys {
		return SiteConfigMutationResult{}, ErrSiteConfigInvalid
	}
	expected, err := strconv.ParseInt(input.ExpectedRevision, 10, 64)
	if err != nil || expected < 0 || strconv.FormatInt(expected, 10) != input.ExpectedRevision {
		return SiteConfigMutationResult{}, ErrSiteConfigInvalid
	}
	tx, err := repository.beginAuthorized(ctx, input.AdminID, false)
	if err != nil {
		return SiteConfigMutationResult{}, err
	}
	defer tx.Rollback()
	keys := make([]string, 0, len(input.Values))
	for key := range input.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	patches := make(map[string]validatedSiteConfigPatch, len(keys))
	wireValues := make(map[string]any, len(keys))
	for _, key := range keys {
		if !knownSiteConfigKey(key) {
			return SiteConfigMutationResult{}, ErrSiteConfigNotFound
		}
		if isSpecializedConfigKey(key) {
			return SiteConfigMutationResult{}, ErrSiteConfigConflict
		}
		patch, err := validateSiteConfigPatch(key, input.Values[key])
		if err != nil {
			return SiteConfigMutationResult{}, err
		}
		patches[key], wireValues[key] = patch, patch.wire
	}
	body, err := idempotency.CanonicalJSON(struct {
		Revision string         `json:"expected_revision"`
		Values   map[string]any `json:"values"`
	}{input.ExpectedRevision, wireValues})
	if err != nil || len(body) > idempotency.MaxControlBodyBytes {
		return SiteConfigMutationResult{}, ErrSiteConfigInvalid
	}
	actorHash, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(input.AdminID, 10))
	if err != nil {
		return SiteConfigMutationResult{}, ErrSiteConfigInvariant
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actorHash, Method: http.MethodPatch, Route: RouteAdminSiteConfig, Body: body})
	if err != nil {
		return SiteConfigMutationResult{}, ErrSiteConfigInvalid
	}
	revision, err := readSiteConfigRevision(ctx, tx)
	if err != nil {
		return SiteConfigMutationResult{}, err
	}
	now := repository.now().UTC().Unix()
	if now < 0 || now > maxSiteConfigUnixSecond-idempotency.ReplayWindowSeconds {
		return SiteConfigMutationResult{}, ErrSiteConfigInvariant
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actorHash, Key: input.IdempotencyKey, RequestHash: digest, DecisionNow: now})
	if err != nil {
		return SiteConfigMutationResult{}, classifySiteConfigIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		var response SiteConfigBatchResponse
		if decision.HTTPStatus != http.StatusOK || json.Unmarshal(decision.ResponseBody, &response) != nil || response.Revision == "" || response.ChangedKeys == nil {
			return SiteConfigMutationResult{}, ErrSiteConfigInvariant
		}
		if err := tx.Commit(); err != nil {
			return SiteConfigMutationResult{}, classifySiteConfigDatabase("commit configuration replay", err)
		}
		return SiteConfigMutationResult{Status: http.StatusOK, Body: decision.ResponseBody, Replayed: true}, nil
	}
	if revision != expected {
		return SiteConfigMutationResult{}, ErrSiteConfigConflict
	}
	stored, err := readKnownSiteConfigRows(ctx, tx)
	if err != nil {
		return SiteConfigMutationResult{}, err
	}
	changed := make([]string, 0, len(keys))
	for _, key := range keys {
		patch := patches[key]
		old, exists := stored[key]
		if (patch.remove && !exists) || (!patch.remove && exists && old == patch.stored) {
			continue
		}
		if key == KeySiteTimezoneOffsetMinutes {
			if err := ensureSiteTimezoneMutable(ctx, tx); err != nil {
				if err == ErrSiteConfigConflict {
					return SiteConfigMutationResult{}, &siteConfigValidationError{message: "The site timezone is locked because daily activity records already exist."}
				}
				return SiteConfigMutationResult{}, err
			}
		}
		changed = append(changed, key)
		if patch.remove {
			delete(stored, key)
		} else {
			stored[key] = patch.stored
		}
	}
	if err := db.ValidateGenerationTwoConfigSnapshot(stored); err != nil {
		return SiteConfigMutationResult{}, siteConfigCombinationError(err)
	}
	if len(changed) > 0 {
		if revision == math.MaxInt64 {
			return SiteConfigMutationResult{}, ErrSiteConfigConflict
		}
		result, err := tx.ExecContext(ctx, `UPDATE config_revisions SET revision=revision+1,updated_at=? WHERE domain='site' AND revision=?`, now, revision)
		if err != nil {
			return SiteConfigMutationResult{}, classifySiteConfigDatabase("advance configuration revision", err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return SiteConfigMutationResult{}, ErrSiteConfigConflict
		}
		for _, key := range changed {
			patch := patches[key]
			if patch.remove {
				_, err = tx.ExecContext(ctx, `DELETE FROM site_config WHERE key=?`, key)
			} else {
				_, err = tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, patch.stored, now)
			}
			if err != nil {
				return SiteConfigMutationResult{}, classifySiteConfigDatabase("write configuration batch", err)
			}
		}
		revision++
	}
	responseBody, err := json.Marshal(SiteConfigBatchResponse{Revision: strconv.FormatInt(revision, 10), ChangedKeys: changed})
	if err != nil {
		return SiteConfigMutationResult{}, fmt.Errorf("%w: encode configuration result", ErrSiteConfigInvariant)
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusOK, responseBody); err != nil {
		return SiteConfigMutationResult{}, classifySiteConfigIdempotencyComplete(err)
	}
	if err := tx.Commit(); err != nil {
		return SiteConfigMutationResult{}, classifySiteConfigDatabase("commit configuration batch", err)
	}
	return SiteConfigMutationResult{Status: http.StatusOK, Body: responseBody}, nil
}
