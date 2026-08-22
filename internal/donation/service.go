// Package donation serves the charity donation rail (frozen §J.1/§J.4/§L,
// implementation contract §2.6/§6.3):
//
//	GET    /api/donations          the caller's own donations
//	POST   /api/donations          submit a donation (existing or nested keys)
//	GET    /api/donations/{id}     one owned donation with keys and reviews
//	PATCH  /api/donations/{id}     edit a still-pending own donation
//	DELETE /api/donations/{id}     soft-delete an own donation
//
// plus a mountable review surface for the administrator and level-5 steward
// frames (shared service, separate routes per §6.4).
//
// Boundary rules:
//
//   - every user route re-checks the user station and the session principal;
//     every review route receives its identity from the mounting frame (admin
//     session or live-resolved level-5 principal) — never from client input;
//   - secrets exist only inside one request: they are validated, sealed
//     contextually by the repository transaction and never echoed, logged or
//     persisted in any recoverable form;
//   - all economic values on the wire are canonical decimal milli-credit
//     strings; counts are JSON numbers; no float ever participates;
//   - new submissions require donation_accept_enabled; every other operation
//     works regardless of that switch (frozen §J.4.5).
package donation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
)

// Site-config switch keys read by this rail.
const (
	SiteConfigDonationAcceptEnabled = "donation_accept_enabled"
)

// Service orchestrates donations over db.Store.
type Service struct {
	store      *db.Store
	urls       BaseURLValidator
	connectors ConnectorValidator
	limiter    LimiterLifecycle
	now        func() int64
	mutationMu sync.Mutex
}

// LimiterLifecycle is implemented by the charity routing service. It is kept
// as a tiny interface so donation persistence remains independently testable.
type LimiterLifecycle interface {
	ForgetDonationKeys(...int64)
	RestoreDonationKeys(...int64)
}

// BaseURLValidator canonicalizes a user-supplied base URL through the egress
// boundary (the production implementation is *egress.EgressPolicy).
type BaseURLValidator interface {
	ValidateBaseURL(raw string) (string, error)
}

// ConnectorValidator validates a connector type against the authoritative
// registry (unknown types rejected, no silent fallback).
type ConnectorValidator interface {
	MustValidate(t endpoint.ConnectorType) (endpoint.ConnectorType, error)
}

// ServiceDeps wire the collaborators.
type ServiceDeps struct {
	Store      *db.Store
	URLs       BaseURLValidator
	Connectors ConnectorValidator
	Limiter    LimiterLifecycle
	Now        func() int64
}

// NewService builds the service. A nil Now defaults to unix seconds.
func NewService(deps ServiceDeps) *Service {
	if deps.Now == nil {
		deps.Now = func() int64 { return time.Now().Unix() }
	}
	return &Service{store: deps.Store, urls: deps.URLs, connectors: deps.Connectors, limiter: deps.Limiter, now: deps.Now}
}

// syncLimiterAfterCommit best-effort reconciles process-local admission state
// from the authoritative post-commit projection. A projection read failure
// leaves the current process state unchanged; routing remains fail-closed via
// the database's authoritative eligibility checks.
func (s *Service) syncLimiterAfterCommit(ctx context.Context, donationID int64) {
	if s == nil || s.limiter == nil {
		return
	}
	states, err := s.store.ListDonationKeyLimiterStates(context.WithoutCancel(ctx), donationID, s.now())
	if err != nil {
		return
	}
	forgetIDs := make([]int64, 0, len(states))
	restoreIDs := make([]int64, 0, len(states))
	for _, state := range states {
		if state.Active && state.Enabled {
			restoreIDs = append(restoreIDs, state.ID)
		} else {
			forgetIDs = append(forgetIDs, state.ID)
		}
	}
	if len(forgetIDs) > 0 {
		s.limiter.ForgetDonationKeys(forgetIDs...)
	}
	if len(restoreIDs) > 0 {
		s.limiter.RestoreDonationKeys(restoreIDs...)
	}
}

// Sentinel errors mapped to stable envelopes at the handler boundary. None of
// them carries request or secret material.
var (
	ErrInvalidRequest        = errors.New("donation: invalid request")
	ErrFeatureDisabled       = errors.New("donation: submission is closed")
	ErrSecretTooLong         = errors.New("donation: secret too long")
	ErrConnectorUnavailable  = errors.New("donation: connector registry unavailable")
	ErrEndpointBaseURLNeeded = errors.New("donation: base URL is required for a nested endpoint")
)

// ExistingKeySelection references already-saved keys of one owned endpoint.
type ExistingKeySelection struct {
	EndpointID int64
	KeyIDs     []int64
	Limits     map[int64]db.KeyLimitSpec // limits per selected physical key id
}

// NewKeyEntry is one freshly entered key of a nested submission.
type NewKeyEntry struct {
	Secret         []byte
	Note           string
	MaxConcurrency int64
	RPMLimit       int64
}

// CreateSpec is the validated service-level payload of one submission.
type CreateSpec struct {
	UserID      int64
	Description string
	ExpiresAt   *int64

	Existing *ExistingKeySelection
	New      *NewEndpointDraft

	// AcceptOpen reports whether donation_accept_enabled was on when the
	// service checked; the repo path itself never gates on the switch so the
	// review surface stays independent of it.
}

// NewEndpointDraft describes the nested personal endpoint.
type NewEndpointDraft struct {
	ConnectorType string
	BaseURL       string
	Note          string
	Enabled       bool
	Keys          []NewKeyEntry
}

// DonationAcceptOpen reads the authoritative intake switch.
func (s *Service) DonationAcceptOpen(ctx context.Context) (bool, error) {
	raw, err := s.store.GetSiteConfigValue(SiteConfigDonationAcceptEnabled)
	if err != nil {
		return false, err
	}
	return raw == "1", nil
}

// Create validates and persists one submission. In the nested mode the base
// URL is canonicalized through the egress boundary and every fresh secret is
// validated and sealed inside the repository's single transaction; a failure —
// including a duplicate physical key claim — rolls back the entire submission.
func (s *Service) Create(ctx context.Context, spec CreateSpec) (db.Donation, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	defer func() {
		if spec.New != nil {
			for i := range spec.New.Keys {
				clear(spec.New.Keys[i].Secret)
			}
		}
	}()
	if spec.UserID <= 0 {
		return db.Donation{}, db.ErrNotFound
	}
	open, err := s.DonationAcceptOpen(ctx)
	if err != nil {
		return db.Donation{}, err
	}
	if !open {
		return db.Donation{}, ErrFeatureDisabled
	}

	in := db.CreateDonationInput{
		UserID:      spec.UserID,
		Description: spec.Description,
		ExpiresAt:   spec.ExpiresAt,
		Now:         s.now(),
	}
	switch {
	case spec.Existing != nil && spec.New == nil:
		if len(spec.Existing.KeyIDs) == 0 {
			return db.Donation{}, fmt.Errorf("%w: at least one key must be selected", ErrInvalidRequest)
		}
		in.Existing = &db.ExistingEndpointKeys{
			EndpointID: spec.Existing.EndpointID,
			KeyIDs:     spec.Existing.KeyIDs,
		}
		for _, id := range spec.Existing.KeyIDs {
			l := spec.Existing.Limits[id]
			in.Limits = append(in.Limits, db.KeyLimitSpec{
				EndpointKeyID: id, MaxConcurrency: l.MaxConcurrency, RPMLimit: l.RPMLimit,
			})
		}
	case spec.New != nil && spec.Existing == nil:
		if len(spec.New.Keys) == 0 {
			return db.Donation{}, fmt.Errorf("%w: at least one key is required", ErrInvalidRequest)
		}
		canonical, cerr := s.canonicalURL(spec.New.BaseURL)
		if cerr != nil {
			return db.Donation{}, cerr
		}
		connector, cerr := s.resolveConnector(spec.New.ConnectorType)
		if cerr != nil {
			return db.Donation{}, cerr
		}
		if nerr := validateNote(spec.New.Note); nerr != nil {
			return db.Donation{}, nerr
		}
		in.New = &db.NewEndpointSpec{
			ConnectorType: string(connector),
			BaseURL:       canonical,
			Note:          spec.New.Note,
			Enabled:       spec.New.Enabled,
		}
		for _, k := range spec.New.Keys {
			if serr := endpoint.ValidateSecret(k.Secret); serr != nil {
				return db.Donation{}, mapSecretErr(serr)
			}
			if nerr := validateNote(k.Note); nerr != nil {
				return db.Donation{}, nerr
			}
			head, tail := endpoint.DisplayFragments(k.Secret)
			in.Keys = append(in.Keys, db.NewKeySpec{
				Secret: k.Secret, Note: k.Note, Enabled: true,
				DisplayHead: head, DisplayTail: tail,
				MaxConcurrency: k.MaxConcurrency, RPMLimit: k.RPMLimit,
			})
		}
	default:
		return db.Donation{}, fmt.Errorf("%w: exactly one endpoint form is required", ErrInvalidRequest)
	}

	d, err := s.store.CreateDonation(ctx, in)
	if err != nil {
		return db.Donation{}, mapRepoError(err)
	}
	s.syncLimiterAfterCommit(ctx, d.ID)
	return d, nil
}

// List returns the caller's donations (one page).
func (s *Service) List(ctx context.Context, userID int64, limit, offset int) ([]db.Donation, int, error) {
	return s.store.ListOwnDonations(ctx, userID, s.now(), limit, offset)
}

// Get returns one owned donation with its keys and review history.
func (s *Service) Get(ctx context.Context, userID, donationID int64) (db.Donation, []db.DonationKey, []db.DonationReview, error) {
	return s.store.GetOwnDonation(ctx, userID, donationID, s.now())
}

// Update edits a pending donation: description, expiry and/or the selected key
// set (same endpoint). Reviewer-side reuse passes role/actor explicitly.
func (s *Service) UpdatePending(ctx context.Context, userID, donationID int64, description *string, expiresAt **int64, keyIDs *[]int64, limits []db.KeyLimitSpec) (db.Donation, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	d, err := s.store.UpdateOwnPendingDonation(ctx, db.UpdateDonationKeysInput{
		UserID: userID, DonationID: donationID, Now: s.now(),
		Description: description, ExpiresAt: expiresAt, KeyIDs: keyIDs, Limits: limits,
	})
	if err != nil {
		return db.Donation{}, mapRepoError(err)
	}
	return d, nil
}

// Delete soft-deletes an own donation.
func (s *Service) Delete(ctx context.Context, userID, donationID int64) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.store.DeleteOwnDonation(ctx, userID, donationID, s.now()); err != nil {
		return mapRepoError(err)
	}
	s.syncLimiterAfterCommit(ctx, donationID)
	return nil
}

// Review applies one reviewer decision (administrator or level-5 steward).
func (s *Service) Review(ctx context.Context, dec db.ReviewDecision) (db.Donation, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if dec.Now == 0 {
		dec.Now = s.now()
	}
	d, err := s.store.ApplyDonationReview(ctx, dec)
	if err != nil {
		return db.Donation{}, mapRepoError(err)
	}
	s.syncLimiterAfterCommit(ctx, d.ID)
	return d, nil
}

// DeleteAsReviewer soft-deletes any donation on behalf of a reviewer.
func (s *Service) DeleteAsReviewer(ctx context.Context, donationID int64, role string, reviewerUserID int64) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.store.DeleteDonationByReviewer(ctx, donationID, role, reviewerUserID, "", s.now()); err != nil {
		return mapRepoError(err)
	}
	s.syncLimiterAfterCommit(ctx, donationID)
	return nil
}

// ListForReview returns one page of donations for a reviewer surface.
func (s *Service) ListForReview(ctx context.Context, status string, limit, offset int) ([]db.Donation, int, error) {
	return s.store.ListReviewableDonations(ctx, status, s.now(), limit, offset)
}

// GetForReview returns one donation with keys and reviews for a reviewer.
func (s *Service) GetForReview(ctx context.Context, donationID int64) (db.Donation, []db.DonationKey, []db.DonationReview, error) {
	return s.store.GetReviewDonation(ctx, donationID, s.now())
}

// FormatMilli renders an economic value as the canonical decimal wire string.
func FormatMilli(v int64) string { return credits.FormatAmount(v) }

// canonicalURL delegates to the egress boundary.
func (s *Service) canonicalURL(raw string) (string, error) {
	if raw == "" {
		return "", ErrEndpointBaseURLNeeded
	}
	if s.urls == nil {
		return "", fmt.Errorf("%w: base URL validator unavailable", ErrInvalidRequest)
	}
	canonical, err := s.urls.ValidateBaseURL(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return canonical, nil
}

// resolveConnector defaults an empty type to openai-compatible and validates
// against the registry (unknown types rejected, no fallback).
func (s *Service) resolveConnector(raw string) (endpoint.ConnectorType, error) {
	t := endpoint.ConnectorType(raw)
	if t == "" {
		t = endpoint.ConnectorOpenAICompatible
	}
	if s.connectors == nil {
		return "", ErrConnectorUnavailable
	}
	validated, err := s.connectors.MustValidate(t)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return validated, nil
}

// validateNote bounds a short note to 256 runes without control characters.
func validateNote(note string) error {
	for _, r := range note {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: note contains control characters", ErrInvalidRequest)
		}
	}
	if len([]rune(note)) > 256 {
		return fmt.Errorf("%w: note too long", ErrInvalidRequest)
	}
	return nil
}

func mapSecretErr(err error) error {
	if errors.Is(err, endpoint.ErrPayloadTooLarge) {
		return ErrSecretTooLong
	}
	return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
}

// mapRepoError translates repository sentinels into the service error set.
func mapRepoError(err error) error {
	var capErr *db.CapError
	if errors.As(err, &capErr) {
		return capErr
	}
	switch {
	case errors.Is(err, db.ErrNotFound), errors.Is(err, db.ErrConflict),
		errors.Is(err, db.ErrDonationKeyClaimConflict),
		errors.Is(err, db.ErrResourceInActiveDonation),
		errors.Is(err, db.ErrInvalidValue):
		return err
	default:
		return fmt.Errorf("donation: repository error: %w", err)
	}
}
