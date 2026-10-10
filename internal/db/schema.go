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

//go:embed migrations/0005_gwent_ai.sql
var gwentAISQL string

//go:embed migrations/0006_discord_gate.sql
var discordGateSQL string

//go:embed migrations/0007_model_types_and_images.sql
var modelTypesAndImagesSQL string

//go:embed migrations/0008_personal_balanced_routing.sql
var personalBalancedRoutingSQL string

//go:embed migrations/0009_gacha.sql
var gachaSQL string

//go:embed migrations/0010_user_appearance.sql
var userAppearanceSQL string

//go:embed migrations/0011_context_pricing.sql
var contextPricingSQL string

//go:embed migrations/0012_session_headers.sql
var sessionHeadersSQL string

//go:embed migrations/0013_personal_adaptation.sql
var personalAdaptationSQL string
