package v1

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"io"
	"testing"
)

//go:embed testdata/schema17.sql.gz
var schema17Compressed []byte

const schema17ManifestHash = "7289b13cd57bb2d6a50a31ffde33cae17ceac2e1e130e0cd759dd2f0c2a98d4b"
const schema17SQLHash = "5d5f8a7c1cc0f30dff338a0dfe81a7b4e99a1fa47788b855fba1c5c74acb4cdf"

func schema17SQLForTest(t *testing.T) string {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(schema17Compressed))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != schema17SQLHash {
		t.Fatal("schema 17 fixture identity changed")
	}
	return string(body)
}

func openSchema17DDLForTest(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(schema17SQLForTest(t)); err != nil {
		t.Fatal(err)
	}
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil || generationManifestDigest(manifest) != schema17ManifestHash {
		t.Fatal("schema 17 source manifest changed", err)
	}
	return database
}
