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

//go:embed migrations/0002_terminal_reservation_indexes.sql
var terminalReservationIndexesSQL string

//go:embed migrations/0003_ai_players.sql
var aiPlayersSQL string

//go:embed migrations/0004_management_and_games.sql
var managementAndGamesSQL string
