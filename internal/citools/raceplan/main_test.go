package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestParseShardSpec(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		value     string
		wantIndex int
		wantTotal int
		wantError bool
	}{
		{name: "first", value: "1/6", wantIndex: 1, wantTotal: 6},
		{name: "last", value: "6/6", wantIndex: 6, wantTotal: 6},
		{name: "missing", wantError: true},
		{name: "zero index", value: "0/6", wantError: true},
		{name: "past end", value: "7/6", wantError: true},
		{name: "zero total", value: "1/0", wantError: true},
		{name: "non canonical", value: "01/6", wantError: true},
		{name: "trailing", value: "1/6/7", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			index, total, err := parseShardSpec(tc.value)
			if tc.wantError {
				if err == nil {
					t.Fatalf("parseShardSpec(%q) unexpectedly succeeded", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseShardSpec(%q): %v", tc.value, err)
			}
			if index != tc.wantIndex || total != tc.wantTotal {
				t.Fatalf("parseShardSpec(%q) = %d/%d, want %d/%d", tc.value, index, total, tc.wantIndex, tc.wantTotal)
			}
		})
	}
}

func TestLoadTimingHintsRejectsMalformedOrInconsistentFiles(t *testing.T) {
	t.Parallel()

	valid := `{
  "version": 1,
  "shards": 6,
  "default_package_seconds": 60,
  "split_packages": ["example/slow"],
  "split_test_counts": {"example/slow": 2},
  "package_seconds": {"example/slow": 120}
}`
	path := writeTimingHints(t, valid)
	hints, err := loadTimingHints(path)
	if err != nil {
		t.Fatalf("load valid hints: %v", err)
	}
	if hints.Shards != 6 || hints.SplitTestCounts["example/slow"] != 2 {
		t.Fatalf("loaded hints = %#v", hints)
	}

	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "trailing value", content: valid + `{}`},
		{name: "unknown field", content: strings.Replace(valid, `"version": 1,`, `"version": 1, "extra": true,`, 1)},
		{name: "missing split count", content: strings.Replace(valid, `{"example/slow": 2}`, `{}`, 1)},
		{name: "extra split count", content: strings.Replace(valid, `{"example/slow": 2}`, `{"example/slow": 2, "example/other": 1}`, 1)},
		{name: "missing split weight", content: strings.Replace(valid, `{"example/slow": 120}`, `{}`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := loadTimingHints(writeTimingHints(t, tc.content)); err == nil {
				t.Fatal("loadTimingHints unexpectedly succeeded")
			}
		})
	}
}

func TestBuildPlansIsDeterministicAndCoversLiveCatalogOnce(t *testing.T) {
	t.Parallel()

	hints := plannerTestHints()
	catalog := []catalogPackage{
		{ImportPath: "example/new"},
		{ImportPath: "example/slow", Tests: []string{"TestOne", "FuzzTwo", "ExampleThree"}},
		{ImportPath: "example/known"},
	}
	reordered := []catalogPackage{
		{ImportPath: "example/known"},
		{ImportPath: "example/slow", Tests: []string{"ExampleThree", "TestOne", "FuzzTwo"}},
		{ImportPath: "example/new"},
	}

	plans, err := buildPlans(catalog, hints, 3)
	if err != nil {
		t.Fatalf("build plans: %v", err)
	}
	again, err := buildPlans(reordered, hints, 3)
	if err != nil {
		t.Fatalf("build reordered plans: %v", err)
	}
	if !reflect.DeepEqual(plans, again) {
		t.Fatalf("plans depend on catalog order:\nfirst:  %#v\nsecond: %#v", plans, again)
	}
	if planDigest(plans) != planDigest(again) {
		t.Fatalf("equivalent plans have different digests: %s != %s", planDigest(plans), planDigest(again))
	}

	var estimated float64
	whole := make(map[string]int)
	tests := make(map[string]int)
	for _, plan := range plans {
		estimated += plan.EstimatedSeconds
		for _, packagePath := range plan.WholePackages {
			whole[packagePath]++
		}
		for packagePath, names := range plan.SplitTests {
			for _, name := range names {
				tests[packagePath+"/"+name]++
			}
		}
	}
	if estimated != 220 {
		t.Fatalf("estimated total = %.1f, want 220.0", estimated)
	}
	if whole["example/known"] != 1 || whole["example/new"] != 1 || len(whole) != 2 {
		t.Fatalf("whole-package coverage = %#v", whole)
	}
	for _, name := range []string{"TestOne", "FuzzTwo", "ExampleThree"} {
		if tests["example/slow/"+name] != 1 {
			t.Fatalf("split coverage for %s = %d, want 1", name, tests["example/slow/"+name])
		}
	}
	if len(tests) != 3 {
		t.Fatalf("split-test coverage = %#v", tests)
	}
}

func TestBuildPlansRejectsUnsafeOrIncompleteCatalogs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		catalog []catalogPackage
		total   int
		want    string
	}{
		{name: "empty", total: 1, want: "catalog is empty"},
		{name: "zero shards", catalog: []catalogPackage{{ImportPath: "example/slow", Tests: []string{"TestOne"}}}, want: "shard count"},
		{name: "missing split package", catalog: []catalogPackage{{ImportPath: "example/other"}}, total: 1, want: "absent from catalog"},
		{name: "test main", catalog: []catalogPackage{{ImportPath: "example/slow", TestMain: true, Tests: []string{"TestOne"}}}, total: 1, want: "declares TestMain"},
		{name: "no tests", catalog: []catalogPackage{{ImportPath: "example/slow"}}, total: 1, want: "has no tests"},
		{name: "invalid test", catalog: []catalogPackage{{ImportPath: "example/slow", Tests: []string{"helper"}}}, total: 1, want: "invalid test name"},
		{name: "duplicate test", catalog: []catalogPackage{{ImportPath: "example/slow", Tests: []string{"TestOne", "TestOne"}}}, total: 1, want: "repeats test"},
		{name: "duplicate package", catalog: []catalogPackage{{ImportPath: "example/slow", Tests: []string{"TestOne"}}, {ImportPath: "example/slow", Tests: []string{"TestTwo"}}}, total: 1, want: "repeats package"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := buildPlans(tc.catalog, plannerTestHints(), tc.total)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("buildPlans() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidatePlansRejectsUnexpectedCoverage(t *testing.T) {
	t.Parallel()

	catalog := []catalogPackage{
		{ImportPath: "example/whole"},
		{ImportPath: "example/slow", Tests: []string{"TestOne"}},
	}
	split := map[string]struct{}{"example/slow": {}}

	for _, tc := range []struct {
		name string
		plan shardPlan
		want string
	}{
		{
			name: "extra whole package",
			plan: shardPlan{Index: 1, WholePackages: []string{"example/whole", "example/extra"}, SplitTests: map[string][]string{"example/slow": {"TestOne"}}},
			want: "unexpected whole package",
		},
		{
			name: "extra split test",
			plan: shardPlan{Index: 1, WholePackages: []string{"example/whole"}, SplitTests: map[string][]string{"example/slow": {"TestOne", "TestTwo"}}},
			want: "unexpected split test",
		},
		{
			name: "wrong index",
			plan: shardPlan{Index: 2, WholePackages: []string{"example/whole"}, SplitTests: map[string][]string{"example/slow": {"TestOne"}}},
			want: "has index",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validatePlans(catalog, split, []shardPlan{tc.plan})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validatePlans() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestExecutionGroupsUseExactTopLevelPatterns(t *testing.T) {
	t.Parallel()

	groups := executionGroups(shardPlan{
		Index:         1,
		WholePackages: []string{"example/a", "example/z"},
		SplitTests: map[string][]string{
			"example/slow": {"ExampleThree", "FuzzTwo", "TestOne", "TestΩ"},
		},
	}, "30m")
	if len(groups) != 2 {
		t.Fatalf("group count = %d, want 2", len(groups))
	}
	wantWhole := []string{"test", "-race", "-count=1", "-timeout=30m", "example/a", "example/z"}
	if !reflect.DeepEqual(groups[0].Args, wantWhole) {
		t.Fatalf("whole args = %#v, want %#v", groups[0].Args, wantWhole)
	}
	if len(groups[1].Args) != 7 || groups[1].Args[4] != "-run" || groups[1].Args[6] != "example/slow" {
		t.Fatalf("split args = %#v", groups[1].Args)
	}
	pattern, err := regexp.Compile(groups[1].Args[5])
	if err != nil {
		t.Fatalf("compile generated pattern: %v", err)
	}
	for _, name := range []string{"ExampleThree", "FuzzTwo", "TestOne", "TestΩ"} {
		if !pattern.MatchString(name) {
			t.Errorf("generated pattern does not match %q", name)
		}
	}
	for _, name := range []string{"Test", "TestOneMore", "prefixTestOne"} {
		if pattern.MatchString(name) {
			t.Errorf("generated pattern unexpectedly matches %q", name)
		}
	}
}

func TestExecuteGroupsContinuesAndAggregatesFailures(t *testing.T) {
	t.Parallel()

	groups := []commandGroup{
		{Label: "first", Args: []string{"first"}},
		{Label: "second", Args: []string{"second"}},
		{Label: "third", Args: []string{"third"}},
	}
	var calls []string
	var output bytes.Buffer
	err := executeGroups(groups, func(args []string) error {
		calls = append(calls, args[0])
		if args[0] != "second" {
			return errors.New("failed")
		}
		return nil
	}, &output)
	if !reflect.DeepEqual(calls, []string{"first", "second", "third"}) {
		t.Fatalf("calls = %#v", calls)
	}
	if err == nil || !strings.Contains(err.Error(), "2 race command(s) failed") ||
		!strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "third") {
		t.Fatalf("executeGroups() error = %v", err)
	}
	if !strings.Contains(output.String(), "running first") || !strings.Contains(output.String(), "finished third") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestPackageHasTestMain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "main_test.go")
	if err := os.WriteFile(path, []byte("package sample\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) {}\n"), 0o600); err != nil {
		t.Fatalf("write test source: %v", err)
	}
	hasMain, err := packageHasTestMain(listedPackage{Dir: dir, TestGoFiles: []string{"main_test.go"}})
	if err != nil {
		t.Fatalf("packageHasTestMain: %v", err)
	}
	if !hasMain {
		t.Fatal("packageHasTestMain = false, want true")
	}
}

func TestValidTopLevelTestName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Test", "TestAlpha", "FuzzΩ", "Example_suffix"} {
		if !validTopLevelTestName(name) {
			t.Errorf("validTopLevelTestName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "helper", "Testhelper", "Fuzzcase", "Examplebad", "Test-with-dash"} {
		if validTopLevelTestName(name) {
			t.Errorf("validTopLevelTestName(%q) = true", name)
		}
	}
}

func plannerTestHints() timingHints {
	return timingHints{
		Version:               1,
		Shards:                3,
		DefaultPackageSeconds: 60,
		SplitPackages:         []string{"example/slow"},
		SplitTestCounts:       map[string]int{"example/slow": 2},
		PackageSeconds: map[string]float64{
			"example/known": 10,
			"example/slow":  100,
		},
	}
}

func writeTimingHints(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "race-timings.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write timing hints: %v", err)
	}
	return path
}
