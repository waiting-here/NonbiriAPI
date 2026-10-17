package db

import (
	"context"
	"database/sql"
	"net/url"
)

const readPoolConnections = 2

// Open readers only after bootstrap has validated the current structure and
// established WAL. Readers share Store ownership and cannot upgrade to writers.
func (s *Store) openReadPool(ctx context.Context, path string) error {
	dsn, err := sqliteFileURI(path, "ro")
	if err != nil {
		return err
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return err
	}
	query := u.Query()
	query.Add("_pragma", "query_only(1)")
	u.RawQuery = query.Encode()
	reader, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	s.readDB = reader
	reader.SetMaxOpenConns(readPoolConnections)
	reader.SetMaxIdleConns(readPoolConnections)
	return reader.PingContext(ctx)
}
