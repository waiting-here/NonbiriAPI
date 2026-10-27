package db

import _ "embed"

// ManagementOperationsSQL creates the independent, retained management history.
//
//go:embed management_operations.sql
var ManagementOperationsSQL string
