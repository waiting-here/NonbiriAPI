package db

import (
	"strings"
	"testing"
)

func TestBrowserAssociationSchemaConstraintsAndCascade(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	user := hostileInsertUser(t, database, "browser-owner", 0, 0)
	digest := strings.Repeat("a", 64)
	hostileMustExec(t, database, "INSERT INTO browser_associations VALUES(?,?,0,1,2,2592000)", user, digest)
	hostileMustFail(t, database, "INSERT INTO browser_associations VALUES(?,?,0,1,2,2592000)", user, digest)
	for _, query := range []string{
		"UPDATE browser_associations SET browser_digest='raw-token'",
		"UPDATE browser_associations SET issued_at=-1",
		"UPDATE browser_associations SET first_seen=3",
		"UPDATE browser_associations SET expires_at=2592001",
		"UPDATE browser_associations SET last_seen=2592000",
	} {
		hostileMustFail(t, database, query)
	}
	hostileMustExec(t, database, "DELETE FROM users WHERE id=?", user)
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM browser_associations").Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted account associations retained", count, err)
	}
}
