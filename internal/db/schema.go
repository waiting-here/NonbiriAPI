// Package db owns the canonical SQLite schema and its supported upgrade paths.
package db

import _ "embed"

// Fresh bootstrap executes this static, non-idempotent DDL without runtime
// transformations. Schema changes update this file's SQL, both independent
// pins, and an append-only schema/data migration. Historical
// declarations belong to the upgrade paths rather than fresh construction.
//
//go:embed generation_two.sql
var generationTwoSchema string
