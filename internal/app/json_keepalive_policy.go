package app

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type jsonKeepaliveSnapshot struct {
	revision int64
	enabled  bool
	interval time.Duration
}

type jsonKeepalivePolicy struct {
	state atomic.Pointer[jsonKeepaliveSnapshot]
}

func loadJSONKeepalivePolicy(ctx context.Context, store *db.Store) (*jsonKeepalivePolicy, error) {
	var revision int64
	var value, interval string
	err := store.ReadDB().QueryRowContext(ctx, `SELECT revision,
		COALESCE((SELECT value FROM site_config WHERE key=?),'0'),
		COALESCE((SELECT value FROM site_config WHERE key=?),'60')
		FROM config_revisions WHERE domain='site'`, adminapi.KeyNonStreamKeepaliveEnabled, adminapi.KeyNonStreamKeepaliveInterval).Scan(&revision, &value, &interval)
	if err != nil {
		return nil, err
	}
	if _, valid := jsonKeepaliveInterval(interval); revision < 1 || value != "0" && value != "1" || !valid {
		return nil, errors.New("invalid non-stream keepalive policy")
	}
	policy := &jsonKeepalivePolicy{}
	policy.committed(adminapi.SiteConfigCommit{Revision: revision, Values: map[string]string{adminapi.KeyNonStreamKeepaliveEnabled: value, adminapi.KeyNonStreamKeepaliveInterval: interval}})
	return policy, nil
}

func (p *jsonKeepalivePolicy) interval() time.Duration {
	if p == nil {
		return 0
	}
	current := p.state.Load()
	if current == nil || !current.enabled {
		return 0
	}
	return current.interval
}

func (p *jsonKeepalivePolicy) committed(change adminapi.SiteConfigCommit) {
	interval, valid := jsonKeepaliveInterval(change.Values[adminapi.KeyNonStreamKeepaliveInterval])
	if !valid {
		return
	}
	next := &jsonKeepaliveSnapshot{revision: change.Revision, enabled: change.Values[adminapi.KeyNonStreamKeepaliveEnabled] == "1", interval: interval}
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

func jsonKeepaliveInterval(value string) (time.Duration, bool) {
	if value == "" {
		return time.Minute, true
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 5 || seconds > 120 || strconv.Itoa(seconds) != value {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}
