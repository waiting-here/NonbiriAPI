package db

import _ "embed"

// AgentDriversSQL stores administrator-owned model profiles and the optional
// ChatGPT connection. It is applied by schema initialization and explicit upgrade.
//
//go:embed agent_drivers.sql
var AgentDriversSQL string
