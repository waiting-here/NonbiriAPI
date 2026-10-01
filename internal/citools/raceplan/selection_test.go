package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestSelectedCatalog(t *testing.T) {
	listed := []listedPackage{{ImportPath: "example/a"}, {ImportPath: "example/b"}, {ImportPath: "example/c"}}
	for _, test := range []struct {
		value           string
		specified, fail bool
		want            []string
	}{
		{specified: false, want: []string{"example/a", "example/b", "example/c"}},
		{value: "example/b, example/a example/b", specified: true, want: []string{"example/a", "example/b"}},
		{specified: true, fail: true},
		{value: "./...", specified: true, fail: true},
		{value: "example/deleted", specified: true, fail: true},
	} {
		selected, err := selectPackages(listed, test.value, test.specified)
		if (err != nil) != test.fail {
			t.Fatalf("selection %q error %v", test.value, err)
		}
		if err != nil {
			continue
		}
		paths := []string{}
		for _, item := range selected {
			paths = append(paths, item.ImportPath)
		}
		if !reflect.DeepEqual(paths, test.want) {
			t.Fatalf("selected %v want %v", paths, test.want)
		}
	}
}

func TestTimingDriftPreservesLiveCoverage(t *testing.T) {
	hints := timingHints{Version: 1, Shards: 3, DefaultPackageSeconds: 1,
		SplitPackages:   []string{"example/split", "example/deleted"},
		SplitTestCounts: map[string]int{"example/split": 2, "example/deleted": 1},
		SplitGroupCaps:  map[string]int{"example/split": 2},
		TestSeconds:     map[string]map[string]float64{"example/split": {"TestOld": 2, "TestRemoved": 3}},
		PackageSeconds:  map[string]float64{"example/split": 4, "example/deleted": 1},
	}
	listed := []listedPackage{{ImportPath: "example/new"}, {ImportPath: "example/split"}}
	var warnings bytes.Buffer
	filtered := catalogHints(listed, hints, &warnings, true)
	catalog := []catalogPackage{{ImportPath: "example/new"}, {ImportPath: "example/split", Tests: []string{"TestOld", "TestNew", "FuzzInput"}}}
	warnTestDrift(catalog, filtered, &warnings)
	plans, err := buildPlans(catalog, filtered, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePlans(catalog, map[string]struct{}{"example/split": {}}, plans); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"unmeasured package example/new", "obsolete package timing example/deleted", "live tests=3 timing baseline=2", "unmeasured test example/split/TestNew", "obsolete test timing example/split/TestRemoved"} {
		if !strings.Contains(warnings.String(), want) {
			t.Fatalf("missing %q in %s", want, warnings.String())
		}
	}
	if !reflect.DeepEqual(hints.SplitPackages, []string{"example/split", "example/deleted"}) || hints.SplitTestCounts["example/deleted"] != 1 {
		t.Fatal("mutated input hints")
	}
	partial := catalogHints(listed[:1], hints, &warnings, false)
	if len(partial.SplitPackages) != 0 {
		t.Fatalf("partial split leakage: %+v", partial)
	}
	if _, err := buildPlans(catalog[:1], partial, 3); err != nil {
		t.Fatal(err)
	}
}

func TestMissingTimingMetadataUsesLiveCoverage(t *testing.T) {
	content := `{"version":1,"shards":2,"default_package_seconds":3,"split_packages":["example/slow"],"split_test_counts":{},"package_seconds":{}}`
	hints, err := loadTimingHints(writeTimingHints(t, content))
	if err != nil {
		t.Fatal(err)
	}
	var warnings bytes.Buffer
	hints = catalogHints([]listedPackage{{ImportPath: "example/slow"}}, hints, &warnings, true)
	catalog := []catalogPackage{{ImportPath: "example/slow", Tests: []string{"TestOne", "TestTwo"}}}
	warnTestDrift(catalog, hints, &warnings)
	fillLiveTestCounts(catalog, hints)
	plans, err := buildPlans(catalog, hints, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePlans(catalog, map[string]struct{}{"example/slow": {}}, plans); err != nil {
		t.Fatal(err)
	}
	if hints.SplitTestCounts["example/slow"] != 2 || !strings.Contains(warnings.String(), "timing baseline=0") {
		t.Fatalf("live fallback: %+v %s", hints, warnings.String())
	}
}
