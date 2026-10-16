// Package db owns the canonical SQLite schema and current storage validation.
package db

import _ "embed"

//go:embed generation_two.sql
var generationTwoSchema string
