package app

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type artworkPolicySnapshot struct {
	revision int64
	disabled bool
}

// Artwork requests read an immutable snapshot instead of querying site_config.
// Committed revisions prevent a delayed callback from restoring an older value.
type artworkRuntimePolicy struct {
	state atomic.Pointer[artworkPolicySnapshot]
}

func loadArtworkPolicy(ctx context.Context, store *db.Store) (*artworkRuntimePolicy, error) {
	var revision int64
	var value string
	err := store.ReadDB().QueryRowContext(ctx, `SELECT revision,
		COALESCE((SELECT value FROM site_config WHERE key=?),'1')
		FROM config_revisions WHERE domain='site'`, adminapi.KeyArtworkR18Disabled).Scan(&revision, &value)
	if err != nil {
		return nil, err
	}
	if revision < 1 || value != "0" && value != "1" {
		return nil, errors.New("invalid artwork policy")
	}
	policy := &artworkRuntimePolicy{}
	policy.committed(adminapi.SiteConfigCommit{Revision: revision, Values: map[string]string{adminapi.KeyArtworkR18Disabled: value}})
	return policy, nil
}

func (p *artworkRuntimePolicy) disabled() bool {
	if p == nil {
		return true
	}
	current := p.state.Load()
	return current == nil || current.disabled
}

func (p *artworkRuntimePolicy) committed(change adminapi.SiteConfigCommit) {
	next := &artworkPolicySnapshot{revision: change.Revision, disabled: change.Values[adminapi.KeyArtworkR18Disabled] != "0"}
	for {
		current := p.state.Load()
		if change.Revision < 1 || current != nil && current.revision >= change.Revision {
			return
		}
		if p.state.CompareAndSwap(current, next) {
			return
		}
	}
}
