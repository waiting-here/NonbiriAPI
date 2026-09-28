package db

// This is the complete deployed schema before library deletion was supported.
const preLevelDeletionManifestHash = "c83b22edad1f51fa26c6bb4a2855c33b9dcf709e81664db42d30453820ed56c5"

// Published versions remain available to existing nodes, challenges and scores.
// The marker removes their editable source from the administrator's library.
const fatFishLevelDeletionSchema = `
CREATE TABLE fatfish_deleted_levels (
 level_id TEXT PRIMARY KEY REFERENCES fatfish_levels(id) ON DELETE CASCADE,
 deleted_at INTEGER NOT NULL CHECK(deleted_at BETWEEN 0 AND 253402300799)
) STRICT;
`
