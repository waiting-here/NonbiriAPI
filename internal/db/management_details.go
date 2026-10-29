package db

import _ "embed"

// ManagementDetailsSQL extends management history with bounded transaction snapshots.
//
//go:embed management_details.sql
var ManagementDetailsSQL string
