package db

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestCanonicalSchemaPreservesDeployedDDLBytes(t *testing.T) {
	const deployedHash = "c692572f9a8c7ba0785808c72a59585d9298d9a1da4b7c90a15878b5a26b9386"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(deployedGenerationTwoSchema))); got != deployedHash {
		t.Fatalf("deployed schema identity changed: %s", got)
	}
	prefix, _, ok := strings.Cut(generationTwoSchema, storageContractsMarker)
	if !ok {
		t.Fatal("canonical additive schema marker is missing")
	}
	for _, change := range storageContractTableChanges {
		if strings.Count(prefix, change.after) != 1 {
			t.Fatalf("unexpected canonical constraint for %s", change.table)
		}
		prefix = strings.Replace(prefix, change.after, change.before, 1)
	}
	if prefix != deployedGenerationTwoSchema {
		t.Fatal("canonical schema differs outside registered additions")
	}
}
