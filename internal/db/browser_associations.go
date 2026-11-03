package db

// BrowserAssociationsSQL is applied by fresh initialization and offline upgrade.
// Tokens and signatures are never persisted.
const BrowserAssociationsSQL = `CREATE TABLE browser_associations (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 browser_digest TEXT NOT NULL CHECK(length(browser_digest)=64 AND browser_digest NOT GLOB '*[^0-9a-f]*'),
 issued_at INTEGER NOT NULL CHECK(issued_at BETWEEN 0 AND 253399708799),
 first_seen INTEGER NOT NULL CHECK(first_seen>=issued_at),
 last_seen INTEGER NOT NULL CHECK(last_seen>=first_seen),
 expires_at INTEGER NOT NULL CHECK(expires_at=issued_at+2592000 AND last_seen<expires_at),
 PRIMARY KEY(user_id,browser_digest)
) STRICT;
CREATE INDEX idx_browser_associations_group ON browser_associations(browser_digest,expires_at,user_id,first_seen,last_seen);
CREATE INDEX idx_browser_associations_expiry ON browser_associations(expires_at);
CREATE INDEX idx_browser_associations_user_recent ON browser_associations(user_id,last_seen DESC,browser_digest);
`
