package attendance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/artwork"
)

type Cover struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Fit   string `json:"fit"`
}

func (c *Cover) artworkKey() string {
	if c != nil {
		switch c.Kind {
		case "pool":
			return "cover:" + c.Value
		case "collection":
			return "illustration:" + c.Value
		}
	}
	return ""
}

func normalizeCover(input *Cover) (*Cover, error) {
	if input == nil || *input == (Cover{}) {
		return nil, nil
	}
	c := *input
	if c.Fit == "" {
		c.Fit = "cover"
	}
	if c.Fit != "cover" && c.Fit != "contain" {
		return nil, fmt.Errorf("%w: select cover or contain for the image fit", ErrInvalid)
	}
	switch c.Kind {
	case "pool", "collection":
		if _, ok := artwork.Rating(c.artworkKey()); !ok {
			return nil, fmt.Errorf("%w: choose an existing built-in cover or collection", ErrInvalid)
		}
	case "external":
		if !validImageURL(c.Value) {
			return nil, fmt.Errorf("%w: enter an HTTPS image URL without credentials, up to 2048 bytes", ErrInvalid)
		}
	default:
		return nil, fmt.Errorf("%w: choose a supported cover type", ErrInvalid)
	}
	return &c, nil
}

// The browser loads the image. Saving a cover never fetches the URL.
func validImageURL(value string) bool {
	if len(value) > 2048 || !utf8.ValidString(value) || strings.ContainsRune(value, '\\') || strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return false
	}
	if port := u.Port(); port != "" {
		_, err = strconv.ParseUint(port, 10, 16)
		if err != nil {
			return false
		}
	}
	return true
}

func (c *Cover) stored() any {
	if c == nil {
		return nil
	}
	raw, _ := json.Marshal(c)
	return string(raw)
}

// Look up the switch at most once in a transaction, and only for rated art.
type coverPolicy struct{ loaded, disabled bool }

func (p *coverPolicy) visible(ctx context.Context, tx *sql.Tx, c *Cover) (bool, error) {
	key := c.artworkKey()
	if key == "" || artwork.Allowed(key, true) {
		return true, nil
	}
	if !p.loaded {
		var err error
		p.disabled, err = artwork.R18DisabledTx(ctx, tx)
		if err != nil {
			return false, err
		}
		p.loaded = true
	}
	return artwork.Allowed(key, p.disabled), nil
}

func (p *coverPolicy) project(ctx context.Context, tx *sql.Tx, period *Period) error {
	allowed, err := p.visible(ctx, tx, period.Cover)
	if err == nil && !allowed {
		period.Cover = nil
	}
	return err
}

func checkCoverChange(ctx context.Context, tx *sql.Tx, next, previous *Cover) error {
	if next.artworkKey() == previous.artworkKey() {
		return nil
	}
	var policy coverPolicy
	allowed, err := policy.visible(ctx, tx, next)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: the selected artwork is disabled", ErrInvalid)
	}
	return nil
}
