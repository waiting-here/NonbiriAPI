package db

import _ "embed"

// AgentPersonalMemorySQL retains administrator-owned memories independently
// of live accounts and conversations.
//
//go:embed agent_personal_memory.sql
var AgentPersonalMemorySQL string
