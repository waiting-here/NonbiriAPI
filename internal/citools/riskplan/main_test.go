package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestVerificationClosure(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for name, body := range map[string]string{
		"internal/leaf/value.go":            "package leaf\n",
		"internal/leaf/testdata/value.json": "{}",
		"internal/ledger/store.go":          "package ledger\n",
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	packages := []packageInfo{
		{ImportPath: "example/internal/leaf", Dir: filepath.Join(root, "internal/leaf")},
		{ImportPath: "example/internal/consumer", Dir: filepath.Join(root, "internal/consumer"), Imports: []string{"example/internal/leaf"}},
		{ImportPath: "example/internal/externaltest", Dir: filepath.Join(root, "internal/externaltest"), Imports: []string{"example/internal/consumer"}},
		{ImportPath: "example/internal/ledger", Dir: filepath.Join(root, "internal/ledger")},
	}
	for _, test := range []struct {
		name            string
		files           []string
		err             error
		full, web, race bool
		expected        []string
	}{
		{name: "transitive and external tests", files: []string{"internal/leaf/value.go"}, expected: []string{"example/internal/consumer", "example/internal/externaltest", "example/internal/leaf"}},
		{name: "fixture owner", files: []string{"internal/leaf/testdata/value.json"}, expected: []string{"example/internal/consumer", "example/internal/externaltest", "example/internal/leaf"}},
		{name: "generated input", files: []string{"internal/leaf/catalog.csv"}, expected: []string{"example/internal/consumer", "example/internal/externaltest", "example/internal/leaf"}},
		{name: "ledger", files: []string{"internal/ledger/store.go"}, race: true, expected: []string{"example/internal/ledger"}},
		{name: "deleted source", files: []string{"internal/leaf/deleted.go"}, full: true, web: true, race: true},
		{name: "unknown", files: []string{"assets/input.bin"}, full: true, web: true, race: true},
		{name: "schema", files: []string{"internal/db/schema.go"}, full: true, web: true, race: true},
		{name: "root wiring", files: []string{"bootstrap.go"}, full: true, web: true, race: true},
		{name: "lock", files: []string{"web/package-lock.json"}, full: true, web: true, race: true},
		{name: "gate", files: []string{"scripts/check-go.sh"}, full: true, web: true, race: true},
		{name: "web", files: []string{"web/src/user/Page.tsx"}, web: true, expected: []string{}},
		{name: "docs", files: []string{"README.md"}, expected: []string{}},
		{name: "empty", full: true, web: true, race: true},
		{name: "bad baseline", files: []string{"README.md"}, err: errors.New("missing"), full: true, web: true, race: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := selectPlan(packages, test.files, "routine", test.err)
			if (plan.Mode == "full") != test.full || plan.Web != test.web || (len(plan.RacePackages) > 0) != test.race {
				t.Fatalf("unexpected scope: %+v", plan)
			}
			if !test.full && !reflect.DeepEqual(plan.GoPackages, test.expected) {
				t.Fatalf("closure %v want %v", plan.GoPackages, test.expected)
			}
			if test.full && len(plan.GoPackages) != len(packages) {
				t.Fatalf("full omitted packages: %+v", plan)
			}
		})
	}
}

func TestConcurrencySyntax(t *testing.T) {
	for _, test := range []struct {
		source string
		want   bool
	}{
		{"package p\n// go routine context sync close\nvar x=1\n", false},
		{"package p\nimport \"sync\"\n", true},
		{"package p\nfunc f(){go f()}\n", true},
		{"package p\nvar c chan int\n", true},
		{"package p\nfunc f(){close(c)}\n", true},
		{"package p\nimport \"context\"\nfunc f(ctx context.Context){}\n", false},
		{"package p\nfunc f(){ctx,cancel:=context.WithCancel(nil); _=ctx;cancel()}\n", true},
		{"not Go", true},
	} {
		if got := concurrentSource([]byte(test.source)); got != test.want {
			t.Errorf("%q got %t", test.source, got)
		}
	}
}

func TestJobOutputs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "output")
	if err := writeJobOutputs(file, verificationPlan{Mode: "routine", GoPackages: []string{"example/a", "example/b"}}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "go=true\nrace=false\nweb=false\nupgrade=false\ngo_packages=example/a example/b\n") {
		t.Fatalf("outputs: %s", body)
	}
}

func TestLiveGraphIncludesPlatformTaggedAndExternalTestImports(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("GOWORK", "off")
	files := map[string]string{
		"go.mod":                  "module example\n\ngo 1.26\n",
		"leaf/value.go":           "package leaf\nconst Value=1\n",
		"platform/value.go":       "package platform\nconst Value=1\n",
		"caller/value.go":         "package caller\n",
		"caller/value_linux.go":   "package caller\nimport _ \"example/platform\"\n",
		"caller/value_windows.go": "package caller\nimport _ \"example/leaf\"\n",
		"caller/tagged.go":        "//go:build custom\n\npackage caller\nimport _ \"example/leaf\"\n",
		"client/value.go":         "package client\n",
		"client/value_test.go":    "package client_test\nimport _ \"example/caller\"\n",
	}
	for name, body := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goTool += ".exe"
	}
	catalog, err := listPackages(goTool)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"leaf/value.go", "platform/value.go"} {
		plan := selectPlan(catalog, []string{file}, "routine", nil)
		if plan.Mode != "routine" || len(plan.GoPackages) != 3 || !slices.Contains(plan.GoPackages, "example/client") || !slices.Contains(plan.GoPackages, "example/caller") {
			t.Fatalf("%s graph: %+v", file, plan)
		}
	}
}

func TestTrustedGitDiffIncludesWorkingInputsAndRejectsMissingBaseline(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	git := func(args ...string) string {
		t.Helper()
		body, err := commandOutput("git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(body))
	}
	git("init", "--quiet")
	if err := os.WriteFile("value.go", []byte("package value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "--", "value.go")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixture")
	base := git("rev-parse", "HEAD")
	git("mv", "value.go", "renamed.go")
	if err := os.WriteFile("generated.csv", []byte("input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := changedFiles(base, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"generated.csv", "renamed.go", "value.go"}) {
		t.Fatalf("working diff: %v", got)
	}
	got, err = changedFiles(base, "HEAD")
	if err != nil || len(got) != 0 {
		t.Fatalf("commit diff: %v %v", got, err)
	}
	for _, bad := range []string{"", "--help", strings.Repeat("0", 40)} {
		if _, err := changedFiles(bad, "HEAD"); err == nil {
			t.Fatalf("accepted baseline %q", bad)
		}
	}
}

func TestSourceRolesAndSharedHelperConcurrencyClosure(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	bodies := map[string]string{
		"internal/db/serial_test.go": `package db
func testHelper() {}
`,
		"internal/helper/testdata/case.json": "{}",
		"internal/db/testdata/case.json":     "{}",
		"root_test.go": `package main
func testHelper() {}
`,
		"internal/helper/value.go": `package helper
func Value() int {return 1}
`,
		"internal/lakenotes/rules/value.go": `package rules
import "context"
func Value(ctx context.Context) int {return 1}
`,
		"internal/game/likes/value.go": `package likes
func Value() int {return 1}
`,
	}
	for name, body := range bodies {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pkgs := []packageInfo{
		{ImportPath: "example", Dir: root},
		{ImportPath: "example/internal/db", Dir: filepath.Join(root, "internal/db")},
		{ImportPath: "example/internal/helper", Dir: filepath.Join(root, "internal/helper")},
		{ImportPath: "example/internal/lakenotes/rules", Dir: filepath.Join(root, "internal/lakenotes/rules")},
		{ImportPath: "example/internal/game/likes", Dir: filepath.Join(root, "internal/game/likes"), Imports: []string{"example/internal/helper", "example/internal/lakenotes/rules"}},
	}
	for _, test := range []struct {
		file string
		race bool
	}{
		{"internal/db/serial_test.go", false},
		{"root_test.go", false},
		{"internal/helper/testdata/case.json", false},
		{"internal/db/testdata/case.json", false},
		{"internal/helper/value.go", true},
		{"internal/lakenotes/rules/value.go", false},
		{"internal/game/likes/value.go", true},
	} {
		plan := selectPlan(pkgs, []string{test.file}, "routine", nil)
		if plan.Mode != "routine" || (len(plan.RacePackages) > 0) != test.race {
			t.Fatalf("%s: %+v", test.file, plan)
		}
	}
	pkgs[2].EmbedFiles = []string{"testdata/case.json"}
	embedded := selectPlan(pkgs, []string{"internal/helper/testdata/case.json"}, "routine", nil)
	if len(embedded.RacePackages) == 0 {
		t.Fatal("production embed lost shared consumer risk")
	}
	for _, name := range []string{"WithCancel", "WithTimeout", "WithTimeoutCause"} {
		if !concurrentSource([]byte("package p;func f(){context." + name + "(nil)}")) {
			t.Fatalf("missing cancellation %s", name)
		}
	}
}
