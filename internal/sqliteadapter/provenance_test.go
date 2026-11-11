package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestReviewedAdapterSources(t *testing.T) {
	raw, err := os.ReadFile("upstream.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		LocalFiles map[string]string `json:"local_files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != manifest.LocalFiles[name] {
			t.Errorf("%s differs from reviewed adapter manifest; rebase the cancellation patch and verify it before updating hashes", name)
		}
		delete(manifest.LocalFiles, name)
	}
	if len(manifest.LocalFiles) != 0 {
		t.Fatalf("missing adapter files: %v", manifest.LocalFiles)
	}
}
