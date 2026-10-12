package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func browserCatalogFixture(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	for _, path := range []string{
		"web/test/e2e/gacha-entrances.spec.ts", "web/test/e2e/gacha-settings.spec.ts",
		"web/test/e2e/linklink-hints.spec.ts", "web/test/e2e/other.spec.ts", "web/test/e2e/new.spec.ts",
		"web/src/user/activities/gacha/api.ts", "web/src/shared/query/client.ts",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(""), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBrowserModuleAndCrossStackScope(t *testing.T) {
	browserCatalogFixture(t)
	for _, path := range []string{"web/src/user/activities/gacha/api.ts", "internal/gacha/http.go"} {
		plan := verificationPlan{Mode: "routine", Changed: []string{path}}
		if err := selectBrowserPlan(&plan); err != nil {
			t.Fatal(err)
		}
		if !plan.Web || len(plan.Browser.Specs) != 2 || !slices.Equal(plan.Browser.Suites, []string{"management"}) {
			t.Fatalf("cross-stack scope %s: %+v", path, plan)
		}
		if path[0:3] == "web" && !slices.Equal(plan.Browser.Related, []string{"src/user/activities/gacha/api.ts"}) {
			t.Fatal(plan.Browser)
		}
	}
}

func TestBrowserUnknownSharedAndDeletedInputsKeepFullCoverage(t *testing.T) {
	browserCatalogFixture(t)
	for _, path := range []string{"web/src/shared/query/client.ts", "internal/newmodule/http.go", "web/test/e2e/deleted.spec.ts"} {
		plan := verificationPlan{Mode: "routine", Changed: []string{path}}
		if err := selectBrowserPlan(&plan); err != nil {
			t.Fatal(err)
		}
		if len(plan.Browser.Specs) != 5 || len(plan.Browser.Suites) != 5 || len(plan.Browser.matrix()) != 4 || !plan.Browser.Engine {
			t.Fatalf("incomplete fallback for %s: %+v", path, plan)
		}
	}
}

func TestBrowserNewSpecAndDocumentationScopes(t *testing.T) {
	browserCatalogFixture(t)
	plan := verificationPlan{Mode: "routine", Changed: []string{"web/test/e2e/new.spec.ts"}}
	if err := selectBrowserPlan(&plan); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Browser.Specs, []string{"test/e2e/new.spec.ts"}) || len(plan.Browser.matrix()) != 1 {
		t.Fatal(plan.Browser)
	}
	for _, path := range []string{"README.zh-CN.md", "CONTRIBUTING.md", "SECURITY.md", "SUPPORT.md", "docs/storage.md"} {
		if !documentationFile(path) {
			t.Fatal(path)
		}
		plan = verificationPlan{Mode: "routine", Changed: []string{path}}
		if err := selectBrowserPlan(&plan); err != nil || plan.Web || len(plan.Browser.matrix()) != 0 {
			t.Fatalf("docs scope: %+v %v", plan, err)
		}
	}
	if documentationFile("internal/db/schema.md") {
		t.Fatal("embedded/package documentation must retain its owner")
	}
}

func TestMissingBrowserCatalogFailsBeforeSelectingNoTests(t *testing.T) {
	t.Chdir(t.TempDir())
	plan := verificationPlan{Mode: "full", Web: true}
	if selectBrowserPlan(&plan) == nil {
		t.Fatal("empty browser catalog accepted")
	}
}

func TestBrowserCatalogIncludesNestedAndRenamedSpecs(t *testing.T) {
	browserCatalogFixture(t)
	path := "web/test/e2e/nested/renamed.spec.ts"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{path, "web/test/e2e/nested/previous.spec.ts"} {
		plan := verificationPlan{Mode: "routine", Changed: []string{changed}}
		if err := selectBrowserPlan(&plan); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(plan.Browser.Specs, "test/e2e/nested/renamed.spec.ts") {
			t.Fatalf("nested or renamed spec omitted: %+v", plan.Browser)
		}
	}
	plan := verificationPlan{Mode: "full", Web: true}
	if err := selectBrowserPlan(&plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Browser.Specs) != 6 {
		t.Fatalf("full catalog: %+v", plan.Browser.Specs)
	}
}

func TestBrowserSharedGameContractsFollowTheirModules(t *testing.T) {
	browserCatalogFixture(t)
	for _, name := range []string{"game-presentation", "frontend-lifecycle"} {
		if err := os.WriteFile("web/test/e2e/"+name+".spec.ts", nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"internal/game/linklink/service.go", "web/src/user/games/linklink/LinkLinkGame.tsx"} {
		plan := verificationPlan{Mode: "routine", Changed: []string{path}}
		if err := selectBrowserPlan(&plan); err != nil {
			t.Fatal(err)
		}
		for _, spec := range []string{"linklink-hints", "game-presentation", "frontend-lifecycle"} {
			if !slices.Contains(plan.Browser.Specs, "test/e2e/"+spec+".spec.ts") {
				t.Fatalf("%s missing %s", path, spec)
			}
		}
	}
	plan := verificationPlan{Mode: "routine", Changed: []string{"internal/gacha/http.go"}}
	if err := selectBrowserPlan(&plan); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Browser.Specs, "test/e2e/frontend-lifecycle.spec.ts") {
		t.Fatal("gacha identity/entry regression omitted")
	}
}
