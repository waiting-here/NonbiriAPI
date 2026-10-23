// Package artwork classifies built-in artwork and its shared source assets.
package artwork

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
)

type ContentRating string

const (
	General ContentRating = "general"
	R18     ContentRating = "r18"
)

type Definition struct {
	Rating ContentRating `json:"rating"`
	Uses   []string      `json:"uses,omitempty"`
	Scene  string        `json:"scene,omitempty"`
	Name   string        `json:"name,omitempty"`
	NameEN string        `json:"name_en,omitempty"`
}

type AssetRule struct {
	Owner    string   `json:"owner"`
	Files    []string `json:"files,omitempty"`
	Prefixes []string `json:"prefixes,omitempty"`
}

type document struct {
	Version  int                   `json:"version"`
	Artworks map[string]Definition `json:"artworks"`
	Assets   []AssetRule           `json:"assets"`
}

type Cover struct {
	ID     string
	Name   string
	NameEN string
}

type prefixRule struct{ prefix, owner string }

// Catalog is immutable after parsing and safe for concurrent lookups.
type Catalog struct {
	definitions map[string]Definition
	ratings     map[string]ContentRating
	files       map[string]string
	prefixes    []prefixRule
	covers      []Cover
}

//go:embed catalog.json
var catalogJSON []byte

var builtIn = func() *Catalog {
	c, err := Parse(catalogJSON)
	if err != nil {
		panic(err)
	}
	return c
}()

func Default() *Catalog                                     { return builtIn }
func Rating(key string) (ContentRating, bool)               { return builtIn.Rating(key) }
func AssetRating(relativePath string) (ContentRating, bool) { return builtIn.AssetRating(relativePath) }
func Allowed(key string, disabled bool) bool                { return builtIn.Allowed(key, disabled) }
func MomentAllowed(key string, disabled bool) bool          { return builtIn.MomentAllowed(key, disabled) }
func Covers() []Cover                                       { return builtIn.Covers() }

// Parse validates references and precomputes effective ratings and path rules.
func Parse(data []byte) (*Catalog, error) {
	var doc document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("artwork: trailing data")
	}
	if doc.Version != 1 || len(doc.Artworks) == 0 {
		return nil, errors.New("artwork: invalid catalog version or entries")
	}
	c := &Catalog{definitions: doc.Artworks, ratings: map[string]ContentRating{}, files: map[string]string{}}
	for key, d := range doc.Artworks {
		kind, id, ok := strings.Cut(key, ":")
		if !ok || id == "" || strings.ContainsAny(id, ":/\\") || !slices.Contains([]string{"card", "illustration", "cover", "appearance"}, kind) {
			return nil, fmt.Errorf("artwork: invalid key %q", key)
		}
		if d.Rating != General && d.Rating != R18 {
			return nil, fmt.Errorf("artwork: invalid rating for %q", key)
		}
		if d.Scene != "" {
			if kind != "card" || !strings.HasPrefix(d.Scene, "illustration:") {
				return nil, fmt.Errorf("artwork: invalid scene for %q", key)
			}
			if _, ok := doc.Artworks[d.Scene]; !ok {
				return nil, fmt.Errorf("artwork: unknown scene for %q", key)
			}
		}
		if kind == "cover" {
			if d.Name == "" || d.NameEN == "" {
				return nil, fmt.Errorf("artwork: unnamed cover %q", key)
			}
			c.covers = append(c.covers, Cover{ID: id, Name: d.Name, NameEN: d.NameEN})
		}
	}
	visiting := map[string]bool{}
	var resolve func(string) (ContentRating, error)
	resolve = func(key string) (ContentRating, error) {
		if value, ok := c.ratings[key]; ok {
			return value, nil
		}
		d, ok := doc.Artworks[key]
		if !ok {
			return "", fmt.Errorf("artwork: unknown reference %q", key)
		}
		if visiting[key] {
			return "", fmt.Errorf("artwork: dependency cycle at %q", key)
		}
		visiting[key] = true
		rating := d.Rating
		for _, dependency := range d.Uses {
			value, err := resolve(dependency)
			if err != nil {
				return "", err
			}
			if value == R18 {
				rating = R18
			}
		}
		visiting[key] = false
		c.ratings[key] = rating
		return rating, nil
	}
	for key := range doc.Artworks {
		if _, err := resolve(key); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"appearance:default", "appearance:heart-moment", "cover:permanent"} {
		if c.ratings[key] != General {
			return nil, fmt.Errorf("artwork: unsafe default %q", key)
		}
	}
	prefixes := map[string]string{}
	add := func(rules map[string]string, value, owner string, prefix bool) error {
		checked := value
		if prefix {
			checked = strings.TrimSuffix(value, "/")
		}
		if !validPath(checked) {
			return fmt.Errorf("artwork: invalid asset path %q", value)
		}
		if previous, ok := rules[value]; ok && previous != owner {
			return fmt.Errorf("artwork: conflicting asset rule %q", value)
		}
		rules[value] = owner
		return nil
	}
	for _, rule := range doc.Assets {
		if _, ok := c.ratings[rule.Owner]; !ok {
			return nil, fmt.Errorf("artwork: unknown asset owner %q", rule.Owner)
		}
		if len(rule.Files)+len(rule.Prefixes) == 0 {
			return nil, errors.New("artwork: empty asset rule")
		}
		for _, file := range rule.Files {
			if err := add(c.files, file, rule.Owner, false); err != nil {
				return nil, err
			}
		}
		for _, prefix := range rule.Prefixes {
			if err := add(prefixes, prefix, rule.Owner, true); err != nil {
				return nil, err
			}
		}
	}
	for prefix, owner := range prefixes {
		c.prefixes = append(c.prefixes, prefixRule{prefix, owner})
	}
	slices.SortFunc(c.prefixes, func(a, b prefixRule) int {
		if n := len(b.prefix) - len(a.prefix); n != 0 {
			return n
		}
		return strings.Compare(a.prefix, b.prefix)
	})
	slices.SortFunc(c.covers, func(a, b Cover) int {
		if a.ID == b.ID {
			return 0
		}
		if a.ID == "permanent" {
			return -1
		}
		if b.ID == "permanent" {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return c, nil
}

func validPath(value string) bool {
	return value != "" && value != "." && !strings.HasPrefix(value, "/") && !strings.ContainsRune(value, 92) && !strings.ContainsAny(value, "?#%") && path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../")
}

func (c *Catalog) Rating(key string) (ContentRating, bool) {
	value, ok := c.ratings[key]
	return value, ok
}
func (c *Catalog) Allowed(key string, disabled bool) bool {
	value, ok := c.Rating(key)
	return ok && (value == General || !disabled)
}
func (c *Catalog) MomentAllowed(key string, disabled bool) bool {
	if !strings.HasPrefix(key, "card:") && !strings.HasPrefix(key, "illustration:") && !strings.HasPrefix(key, "cover:") {
		return false
	}
	if !c.Allowed(key, disabled) {
		return false
	}
	scene := c.definitions[key].Scene
	return scene == "" || c.Allowed(scene, disabled)
}
func (c *Catalog) AssetRating(relativePath string) (ContentRating, bool) {
	if !validPath(relativePath) {
		return "", false
	}
	if owner, ok := c.files[relativePath]; ok {
		return c.Rating(owner)
	}
	for _, rule := range c.prefixes {
		if strings.HasPrefix(relativePath, rule.prefix) {
			return c.Rating(rule.owner)
		}
	}
	return "", false
}
func (c *Catalog) Covers() []Cover { return slices.Clone(c.covers) }

// R18DisabledTx reads the canonical policy in the transaction that selects art.
// Missing configuration retains the safe installation default.
func R18DisabledTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var value string
	err := tx.QueryRowContext(ctx, "SELECT value FROM site_config WHERE key='artwork_r18_disabled'").Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	switch value {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return true, errors.New("artwork: invalid site policy")
	}
}
