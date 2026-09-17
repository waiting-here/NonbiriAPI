package stewardautomation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type failurePolicyInput struct {
	DonationID       string `json:"donation_id"`
	DonationKeyID    string `json:"donation_key_id"`
	ExpectedRevision string `json:"expected_revision"`
	Threshold        string `json:"failure_disable_threshold"`
}

func (s *Service) failurePolicyHTTP(w http.ResponseWriter, r *http.Request, userID int64) {
	if r.Method == http.MethodGet {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 2 || len(query["donation_id"]) != 1 || len(query["donation_key_id"]) != 1 {
			writeError(w, errInvalid)
			return
		}
		donationID, err := numericID(query.Get("donation_id"))
		if err != nil {
			writeError(w, errInvalid)
			return
		}
		keyID, err := numericID(query.Get("donation_key_id"))
		if err != nil {
			writeError(w, errInvalid)
			return
		}
		if r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil || len(body) != 0 {
				writeError(w, errInvalid)
				return
			}
		}
		tx, err := s.begin(r.Context(), userID)
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback()
		value, err := s.donations.ReadFailurePolicyStewardInTransaction(r.Context(), tx, userID, donationID, keyID)
		if err != nil {
			writeError(w, err)
			return
		}
		body, err := json.Marshal(value)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || r.Body == nil {
		writeError(w, errInvalid)
		return
	}
	if _, err := idempotency.KeyHash(keys[0]); err != nil {
		writeError(w, errInvalid)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, idempotency.MaxControlBodyBytes))
	defer clear(body)
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeError(w, errInvalid)
		}
		return
	}
	if strictjson.ValidateObjectWithFieldLimit(body, 16384) != nil {
		writeError(w, errInvalid)
		return
	}
	var input failurePolicyInput
	if decode(body, &input) != nil {
		writeError(w, errInvalid)
		return
	}
	canonical, err := canonicalJSON(body)
	if err != nil {
		writeError(w, errInvalid)
		return
	}
	defer clear(canonical)
	out, err := s.setFailurePolicy(r.Context(), userID, keys[0], canonical, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) setFailurePolicy(ctx context.Context, userID int64, key string, canonical []byte, input failurePolicyInput) ([]byte, error) {
	donationID, err := numericID(input.DonationID)
	if err != nil {
		return nil, errInvalid
	}
	keyID, err := numericID(input.DonationKeyID)
	if err != nil {
		return nil, errInvalid
	}
	expected, err := numericID(input.ExpectedRevision)
	if err != nil {
		return nil, errInvalid
	}
	if _, err := db.ParseU128Decimal(input.Threshold); err != nil {
		return nil, errInvalid
	}
	tx, err := s.begin(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Revalidate both the CallerKey generation and the current object boundary
	// before consulting a cached mutation response.
	if _, err := s.donations.ReadFailurePolicyStewardInTransaction(ctx, tx, userID, donationID, keyID); err != nil {
		return nil, err
	}
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(userID, 10))
	if err != nil {
		return nil, err
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: http.MethodPatch, Route: FailurePolicyPath, Body: canonical})
	if err != nil {
		return nil, errInvalid
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: key, RequestHash: digest, DecisionNow: time.Now().Unix()})
	if err != nil {
		return nil, err
	}
	if decision.Kind == idempotency.Replay {
		return decision.ResponseBody, nil
	}
	value, err := s.donations.SetFailurePolicyStewardInTransaction(ctx, tx, userID, donationID, keyID, expected, input.Threshold)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusOK, body); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return body, nil
}
