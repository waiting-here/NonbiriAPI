package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerFixtureEnvironmentReplacesInheritedValues(t *testing.T) {
	fixture := runnerFixture{
		path:     filepath.Join(t.TempDir(), strings.Repeat("a", 64)+".sqlite"),
		identity: strings.Repeat("a", 64),
		sha256:   strings.Repeat("b", 64),
	}
	parent := []string{
		"UNRELATED=preserved",
		raceTemplatePathEnv + "=stale",
		raceTemplateSHAEnv + "=stale",
		raceTemplateIDEnv + "=stale",
	}
	child := fixture.childEnvironment(parent)
	if len(child) != 4 || child[0] != "UNRELATED=preserved" {
		t.Fatalf("child environment = %#v", child)
	}
	for _, want := range []string{
		raceTemplatePathEnv + "=" + fixture.path,
		raceTemplateSHAEnv + "=" + fixture.sha256,
		raceTemplateIDEnv + "=" + fixture.identity,
	} {
		found := false
		for _, value := range child {
			found = found || value == want
		}
		if !found {
			t.Fatalf("child environment missing %q: %#v", want, child)
		}
	}
}

func TestPrepareRunnerFixtureUsesUniquePrivateRunIdentity(t *testing.T) {
	if os.Getenv("NONBIRI_RACE_FIXTURE_INTEGRATION") != "1" {
		t.Skip("run explicitly to verify the closed template child builder")
	}
	goTool := os.Getenv("GO")
	if goTool == "" {
		goTool = "go"
	}
	first, err := prepareRunnerFixture(goTool, "plan-one")
	if err != nil {
		t.Fatalf("prepare first runner fixture: %v", err)
	}
	t.Cleanup(func() { _ = first.cleanup() })
	second, err := prepareRunnerFixture(goTool, "plan-two")
	if err != nil {
		t.Fatalf("prepare second runner fixture: %v", err)
	}
	t.Cleanup(func() { _ = second.cleanup() })
	if first.path == second.path || first.identity == second.identity || first.dir == second.dir {
		t.Fatalf("distinct runs reused one fixture: first=%s second=%s", first.path, second.path)
	}
	t.Logf("race fixture builds: first=%s second=%s bytes=%d/%d", first.buildTime, second.buildTime, first.size, second.size)
	for _, fixture := range []runnerFixture{first, second} {
		info, err := os.Lstat(fixture.path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("prepared fixture missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(fixture.dir, "template.sqlite")); !os.IsNotExist(err) {
			t.Fatalf("temporary builder output remained: %v", err)
		}
	}
}
