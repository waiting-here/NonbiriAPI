package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
  "split_group_caps": {"example/slow": 4},
  "test_seconds": {"example/slow": {"TestOne": 90}},
  "package_seconds": {"example/slow": 120}
}`
	path := writeTimingHints(t, valid)
	hints, err := loadTimingHints(path)
	if err != nil {
		t.Fatalf("load valid hints: %v", err)
	}
	if hints.Shards != 6 || hints.SplitTestCounts["example/slow"] != 2 || hints.SplitGroupCaps["example/slow"] != 4 || hints.TestSeconds["example/slow"]["TestOne"] != 90 {
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
		{name: "unsplit group cap", content: strings.Replace(valid, `{"example/slow": 4}`, `{"example/whole": 4}`, 1)},
		{name: "zero group cap", content: strings.Replace(valid, `{"example/slow": 4}`, `{"example/slow": 0}`, 1)},
		{name: "negative group cap", content: strings.Replace(valid, `{"example/slow": 4}`, `{"example/slow": -1}`, 1)},
		{name: "fractional group cap", content: strings.Replace(valid, `{"example/slow": 4}`, `{"example/slow": 1.5}`, 1)},
		{name: "missing split weight", content: strings.Replace(valid, `{"example/slow": 120}`, `{}`, 1)},
		{name: "invalid test name", content: strings.Replace(valid, `{"TestOne": 90}`, `{"helper": 90}`, 1)},
		{name: "invalid test weight", content: strings.Replace(valid, `{"TestOne": 90}`, `{"TestOne": 0}`, 1)},
		{name: "unsplit test weights", content: strings.Replace(valid, `{"example/slow": {"TestOne": 90}}`, `{"example/whole": {"TestOne": 90}}`, 1)},
		{name: "empty test weights", content: strings.Replace(valid, `{"example/slow": {"TestOne": 90}}`, `{"example/slow": {}}`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := loadTimingHints(writeTimingHints(t, tc.content)); err == nil {
				t.Fatal("loadTimingHints unexpectedly succeeded")
			}
		})
	}
}

func TestBuildPlansUseMeasuredWeightsAndConservativeNewTestWeight(t *testing.T) {
	t.Parallel()

	hints := plannerTestHints()
	hints.TestSeconds = map[string]map[string]float64{
		"example/slow": {"TestOne": 90, "TestTwo": 80},
	}
	catalog := []catalogPackage{
		{ImportPath: "example/known"},
		{ImportPath: "example/new"},
		{ImportPath: "example/slow", Tests: []string{"TestOne", "TestTwo", "TestNew"}},
	}
	plans, err := buildPlans(catalog, hints, 3)
	if err != nil {
		t.Fatalf("build plans: %v", err)
	}

	shardFor := make(map[string]int)
	for _, plan := range plans {
		for _, testName := range plan.SplitTests["example/slow"] {
			shardFor[testName] = plan.Index
		}
	}
	if shardFor["TestOne"] == 0 || shardFor["TestTwo"] == 0 || shardFor["TestNew"] == 0 {
		t.Fatalf("measured/fallback tests missing from plan: %#v", shardFor)
	}
	if shardFor["TestOne"] == shardFor["TestTwo"] {
		t.Fatalf("measured heavy tests share a shard: %#v", shardFor)
	}

	var estimated float64
	for _, plan := range plans {
		estimated += plan.EstimatedSeconds
	}
	// 90 + 80 are measured overrides; TestNew keeps the 100/2 fallback,
	// while the two whole packages contribute 10 and the default 60.
	if estimated != 290 {
		t.Fatalf("estimated total = %.1f, want 290.0", estimated)
	}
	if plans[2].EstimatedSeconds != 110 {
		t.Fatalf("fallback placement estimate = %.1f, want 110.0", plans[2].EstimatedSeconds)
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
	if planDigest(plans, "30m", 4) != planDigest(again, "30m", 4) {
		t.Fatalf("equivalent plans have different digests: %s != %s", planDigest(plans, "30m", 4), planDigest(again, "30m", 4))
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
	}, "30m", 4)
	if len(groups) != 3 {
		t.Fatalf("group count = %d, want 3", len(groups))
	}
	wantWhole := []string{"test", "-race", "-shuffle=on", "-count=1", "-timeout=30m", "-p=1", "-v", "example/a"}
	if !reflect.DeepEqual(groups[0].Args, wantWhole) {
		t.Fatalf("whole args = %#v, want %#v", groups[0].Args, wantWhole)
	}
	if groups[1].Args[len(groups[1].Args)-1] != "example/z" {
		t.Fatalf("second whole args = %#v", groups[1].Args)
	}
	if len(groups[2].Args) != 10 || groups[2].Args[7] != "-run" || groups[2].Args[9] != "example/slow" {
		t.Fatalf("split args = %#v", groups[2].Args)
	}
	pattern, err := regexp.Compile(groups[2].Args[len(groups[2].Args)-2])
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

func TestExecutionGroupsSplitByWeightDeterministicallyAndExactlyOnce(t *testing.T) {
	t.Parallel()
	plan := shardPlan{
		WholePackages:       []string{"example/whole"},
		WholePackageWeights: map[string]float64{"example/whole": 20},
		SplitTests:          map[string][]string{"example/slow": {"TestLight", "TestHeavy", "TestMedium", "TestOther"}},
		SplitTestWeights: map[string]map[string]float64{"example/slow": {
			"TestLight": 1, "TestHeavy": 10, "TestMedium": 5, "TestOther": 2,
		}},
		SplitGroupCaps: map[string]int{"example/slow": 4},
	}
	first := executionGroups(plan, "30m", 2)
	plan.SplitTests["example/slow"] = []string{"TestOther", "TestMedium", "TestHeavy", "TestLight"}
	second := executionGroups(plan, "30m", 2)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("grouping is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if len(first) != 3 || first[1].Label != "example/slow group 1/2" || first[2].Label != "example/slow group 2/2" {
		t.Fatalf("groups = %#v", first)
	}
	seen := make(map[string]int)
	for _, group := range first[1:] {
		pattern, err := regexp.Compile(group.Args[len(group.Args)-2])
		if err != nil {
			t.Fatalf("compile %s: %v", group.Label, err)
		}
		for _, name := range []string{"TestLight", "TestHeavy", "TestMedium", "TestOther"} {
			if pattern.MatchString(name) {
				seen[name]++
			}
		}
	}
	for _, name := range []string{"TestLight", "TestHeavy", "TestMedium", "TestOther"} {
		if seen[name] != 1 {
			t.Fatalf("test %s matched %d groups", name, seen[name])
		}
	}
}

func TestBuildPlansApplyPackageGroupCapsAndCoverEveryTest(t *testing.T) {
	t.Parallel()
	catalog := []catalogPackage{
		{ImportPath: "example/fixture", Tests: []string{"TestOne", "TestTwo", "TestThree", "TestFour", "TestFive", "TestSix"}},
		{ImportPath: "example/migration", Tests: []string{"TestOne", "TestTwo", "TestThree", "TestFour", "TestFive", "TestSix"}},
	}
	hints := timingHints{
		Version:               1,
		Shards:                2,
		DefaultPackageSeconds: 60,
		SplitPackages:         []string{"example/fixture", "example/migration"},
		SplitTestCounts:       map[string]int{"example/fixture": 6, "example/migration": 6},
		SplitGroupCaps:        map[string]int{"example/migration": 4},
		PackageSeconds:        map[string]float64{"example/fixture": 60, "example/migration": 60},
	}
	plans, err := buildPlans(catalog, hints, 2)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]int)
	for _, plan := range plans {
		groups := executionGroups(plan, "30m", 4)
		counts := make(map[string]int)
		for _, group := range groups {
			packagePath := group.Args[len(group.Args)-1]
			counts[packagePath]++
			pattern := regexp.MustCompile(group.Args[len(group.Args)-2])
			for _, pkg := range catalog {
				if pkg.ImportPath != packagePath {
					continue
				}
				for _, name := range pkg.Tests {
					if pattern.MatchString(name) {
						seen[packagePath+"/"+name]++
					}
				}
			}
		}
		if counts["example/fixture"] != 1 || counts["example/migration"] != 3 {
			t.Fatalf("shard %d group counts = %#v, want one fixture process and three migration processes", plan.Index, counts)
		}
	}
	for _, pkg := range catalog {
		for _, name := range pkg.Tests {
			key := pkg.ImportPath + "/" + name
			if seen[key] != 1 {
				t.Errorf("%s matched %d groups, want 1", key, seen[key])
			}
		}
	}
	for _, caps := range []map[string]int{
		{"example/fixture": 0},
		{"example/fixture": -1},
		{"example/unknown": 2},
	} {
		hints.SplitGroupCaps = caps
		if _, err := buildPlans(catalog, hints, 2); err == nil {
			t.Errorf("accepted invalid caps %#v", caps)
		}
	}
}

func TestPlanDigestPinsEffectiveExecutionPolicy(t *testing.T) {
	t.Parallel()
	plan := shardPlan{
		Index:      1,
		SplitTests: map[string][]string{"example/slow": {"TestOne", "TestTwo", "TestThree", "TestFour"}},
	}
	digest := func(timeout string, workers int) string {
		return planDigest([]shardPlan{plan}, timeout, workers)
	}
	defaultDigest := digest("30m", 4)
	plan.SplitGroupCaps = map[string]int{"example/slow": 1}
	if got := digest("30m", 4); got != defaultDigest {
		t.Fatalf("explicit default changes digest: %s != %s", got, defaultDigest)
	}
	plan.SplitGroupCaps["example/slow"] = 4
	fourGroups := digest("30m", 4)
	if fourGroups == defaultDigest {
		t.Fatal("different process grouping has the same digest")
	}
	plan.SplitGroupCaps["example/slow"] = 8
	if got := digest("30m", 4); got != fourGroups {
		t.Fatalf("ineffective cap changes digest: %s != %s", got, fourGroups)
	}
	if got := digest("30m", 2); got == fourGroups {
		t.Fatal("different worker policy has the same digest")
	}
	if got := digest("20m", 4); got == fourGroups {
		t.Fatal("different command deadline has the same digest")
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
	err := executeGroups(groups, 1, func(group commandGroup, _ io.Writer) error {
		calls = append(calls, group.Args[0])
		if group.Args[0] != "second" {
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

func TestExecuteGroupsBoundsConcurrencyAndRunsEveryGroup(t *testing.T) {
	t.Parallel()
	groups := make([]commandGroup, 7)
	for index := range groups {
		groups[index] = commandGroup{Label: fmt.Sprintf("group-%d", index), Args: []string{fmt.Sprintf("%d", index)}, Weight: float64(index + 1)}
	}
	var active, maximum atomic.Int32
	var callsMu sync.Mutex
	calls := make([]string, 0, len(groups))
	started := make(chan string, len(groups))
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- executeGroups(groups, 4, func(group commandGroup, output io.Writer) error {
			current := active.Add(1)
			for {
				previous := maximum.Load()
				if current <= previous || maximum.CompareAndSwap(previous, current) {
					break
				}
			}
			callsMu.Lock()
			calls = append(calls, group.Label)
			callsMu.Unlock()
			started <- group.Label
			<-release
			active.Add(-1)
			if group.Label == "group-2" || group.Label == "group-5" {
				return errors.New("failed")
			}
			_, _ = io.WriteString(output, group.Label+" output\n")
			return nil
		}, io.Discard)
	}()
	seenStarted := make(map[string]struct{}, 4)
	for index := 0; index < 4; index++ {
		label := <-started
		if _, duplicate := seenStarted[label]; duplicate {
			t.Fatalf("group %s started twice in first worker batch", label)
		}
		seenStarted[label] = struct{}{}
	}
	for _, label := range []string{"group-3", "group-4", "group-5", "group-6"} {
		if _, ok := seenStarted[label]; !ok {
			t.Fatalf("heavy group %s missing from first batch: %v", label, seenStarted)
		}
	}
	select {
	case label := <-started:
		t.Fatalf("group %s started before a worker was released", label)
	default:
	}
	close(release)
	err := <-done
	if maximum.Load() != 4 {
		t.Fatalf("maximum concurrency = %d, want 4", maximum.Load())
	}
	if len(calls) != len(groups) {
		t.Fatalf("executed %d groups, want %d", len(calls), len(groups))
	}
	seenCalls := make(map[string]struct{}, len(calls))
	for _, label := range calls {
		if _, duplicate := seenCalls[label]; duplicate {
			t.Fatalf("group %s executed twice", label)
		}
		seenCalls[label] = struct{}{}
	}
	if err == nil || !strings.Contains(err.Error(), "2 race command(s) failed") || !strings.Contains(err.Error(), "group-2") || !strings.Contains(err.Error(), "group-5") {
		t.Fatalf("executeGroups() error = %v", err)
	}
}

func TestExecuteGroupsRejectsInvalidWorkerCount(t *testing.T) {
	t.Parallel()
	for _, workers := range []int{0, -1} {
		if err := executeGroups(nil, workers, func(commandGroup, io.Writer) error { return nil }, io.Discard); err == nil {
			t.Fatalf("workers=%d unexpectedly accepted", workers)
		}
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
