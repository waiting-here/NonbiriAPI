package db

import _ "embed"

// ManagementServiceHealthSQL adds bounded snapshots for managed service recovery.
//
//go:embed management_service_health.sql
var ManagementServiceHealthSQL string
