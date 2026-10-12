// Package db owns the canonical SQLite schema and its supported upgrade paths.
package db

import _ "embed"

//go:embed generation_two.sql
var generationTwoSchema string
