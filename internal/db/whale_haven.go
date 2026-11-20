package db

import _ "embed"

// WhaleHavenSQL is shared by fresh databases and the offline schema-54 upgrade.
//
//go:embed whale_haven.sql
var WhaleHavenSQL string
