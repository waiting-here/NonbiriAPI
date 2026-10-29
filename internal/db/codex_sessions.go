package db

import _ "embed"

// CodexSessionSQL stores the administrator-owned Codex connection. It is
// installed only by current-schema initialization or explicit upgrade.
//
//go:embed codex_sessions.sql
var CodexSessionSQL string
