package stewardautomation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) create(ctx context.Context, userID int64, key string, canonical []byte, input createInput) ([]byte, error) {
	if input.Endpoint == nil ||
		strings.TrimSpace(input.Description) == "" || len(input.Keys) < 1 || len(input.Keys) > maxKeys {
		return nil, errInvalid
	}
	seen := make(map[[32]byte]bool, len(input.Keys))
	for index, item := range input.Keys {
		digest := sha256.Sum256([]byte(item.Secret))
		if item.Secret == "" || seen[digest] || len(item.RecurringLimits) > donationquota.MaxRules {
			return nil, atStep(fmt.Sprintf("keys[%d]", index), errInvalid)
		}
		seen[digest] = true
		for _, rule := range item.RecurringLimits {
			if rule.ID != nil || donationquota.Validate(rule) != nil {
				return nil, atStep(fmt.Sprintf("keys[%d].recurring_limits", index), errInvalid)
			}
		}
	}
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(userID, 10))
	if err != nil {
		return nil, err
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: http.MethodPost, Route: DonationsPath, Body: canonical})
	if err != nil {
		return nil, errInvalid
	}
	tx, err := s.begin(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: key, RequestHash: digest, DecisionNow: now})
	if err != nil {
		return nil, err
	}
	if decision.Kind == idempotency.Replay {
		return decision.ResponseBody, nil
	}
	ep, err := s.resources.CreateEndpointInTransaction(ctx, tx, userID, resources.CreateEndpointInput{
		Source: "custom", ConnectorType: input.Endpoint.ConnectorType, BaseURL: input.Endpoint.BaseURL, Note: input.Endpoint.Note, Enabled: enabled(input.Endpoint.Enabled),
	})
	if err != nil {
		return nil, atStep("endpoint", err)
	}
	endpointID, err := numericID(ep.ID)
	if err != nil {
		return nil, err
	}
	result := createdDonation{EndpointID: ep.ID, Keys: make([]createdKey, len(input.Keys))}
	donationKeys := make([]donation.CreateKeyInput, len(input.Keys))
	for index, item := range input.Keys {
		secret := []byte(item.Secret)
		physical, createErr := s.resources.CreateEndpointKeyInTransaction(ctx, tx, userID, endpointID, resources.CreateEndpointKeyInput{
			Secret: secret, Note: item.Note, Enabled: enabled(item.Enabled), ForceStoreFalse: item.ForceStoreFalse, OwnershipConfirmed: true, MaxConcurrency: item.MaxConcurrency, MaxRPM: item.MaxRPM,
		})
		clear(secret)
		if createErr != nil {
			return nil, atStep(fmt.Sprintf("keys[%d]", index), createErr)
		}
		physicalID, err := numericID(physical.ID)
		if err != nil {
			return nil, err
		}
		donationKeys[index] = donation.CreateKeyInput{EndpointKeyID: physicalID, ExpiresAt: item.AuthorizedExpiresAt, FailureDisableThreshold: item.FailureDisableThreshold}
		result.Keys[index].EndpointKeyID = physical.ID
	}
	submission, err := s.donations.CreateInTransaction(ctx, tx, userID, donation.CreateInput{Description: input.Description, Keys: donationKeys, OwnershipAuthorized: true})
	if err != nil {
		return nil, atStep("donation", err)
	}
	result.DonationID = submission.ID
	donationID, err := numericID(submission.ID)
	if err != nil {
		return nil, err
	}
	byPhysical := make(map[string]string, len(submission.Keys))
	for _, item := range submission.Keys {
		if item.EndpointKeyID != nil {
			byPhysical[*item.EndpointKeyID] = item.ID
		}
	}
	settings := make([]donation.KeySetting, len(input.Keys))
	for index, item := range input.Keys {
		idText := byPhysical[result.Keys[index].EndpointKeyID]
		id, err := numericID(idText)
		if err != nil {
			return nil, err
		}
		result.Keys[index].DonationKeyID = idText
		expires := item.AuthorizedExpiresAt
		if item.ExpiresAt.set {
			expires = item.ExpiresAt.value
		}
		settings[index] = donation.KeySetting{DonationKeyID: id, PriceLimit: item.PriceLimit, CallsLimit: item.CallsLimit, TokensLimit: item.TokensLimit,
			TokenReserve: item.TokenReserve, Enabled: enabled(item.CharityEnabled), SafeNote: item.SafeNote, ExpiresAt: expires}
	}
	if err := s.donations.ApproveOwnNewInTransaction(ctx, tx, userID, donationID, donation.ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: input.ReviewNote, KeySettings: settings}); err != nil {
		return nil, atStep("approval", err)
	}
	quotaNow := time.Now().Unix()
	for index, item := range input.Keys {
		if err := donationquota.Replace(ctx, tx, settings[index].DonationKeyID, quotaNow, item.RecurringLimits); err != nil {
			return nil, atStep(fmt.Sprintf("keys[%d].recurring_limits", index), err)
		}
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusCreated, body); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return body, nil
}
