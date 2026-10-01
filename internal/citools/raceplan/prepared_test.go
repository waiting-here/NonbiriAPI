package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestExactRiskExclusionsKeepNewTestsAndRejectDrift(t *testing.T) {
	catalog := []catalogPackage{{ImportPath: "example/db", Tests: []string{"TestDDL", "TestCloseRace", "TestNew"}}}
	exclusion := raceExclusion{Package: "example/db", Test: "TestDDL", Reason: "serial DDL"}
	selected, applied, err := filterRiskTests(catalog, []raceExclusion{exclusion}, true)
	if err != nil || len(applied) != 1 || !reflect.DeepEqual(selected[0].Tests, []string{"TestCloseRace", "TestNew"}) {
		t.Fatalf("selection: %+v %+v %v", selected, applied, err)
	}
	for _, bad := range []raceExclusion{
		{Package: "example/db", Test: "TestGone", Reason: "serial"},
		{Package: "example/gone", Test: "TestDDL", Reason: "serial"},
		{Package: "example/db", Test: "TestDDL"},
		{Package: "example/db", Test: ".*", Reason: "serial"},
	} {
		if _, _, err := filterRiskTests(catalog, []raceExclusion{bad}, true); err == nil {
			t.Fatalf("accepted bad exclusion %+v", bad)
		}
	}
	if _, _, err := filterRiskTests(catalog, []raceExclusion{exclusion, exclusion}, true); err == nil {
		t.Fatal("duplicate accepted")
	}
	catalog[0].TestMain = true
	if _, _, err := filterRiskTests(catalog, []raceExclusion{exclusion}, true); err == nil {
		t.Fatal("TestMain partly excluded")
	}
}

func TestReviewedPolicyRetainsDatabaseLifecycleAndEngineCancellation(t *testing.T) {
	var groups []exclusionGroup
	if err := json.Unmarshal(exclusionJSON, &groups); err != nil {
		t.Fatal(err)
	}
	excluded := map[string]bool{}
	for _, group := range groups {
		if group.Reason == "" || len(group.Tests) == 0 {
			t.Fatal("missing reason/tests")
		}
		for _, test := range group.Tests {
			key := group.Package + "/" + test
			if excluded[key] {
				t.Fatalf("duplicate %s", key)
			}
			excluded[key] = true
		}
	}
	for _, item := range []struct {
		pkg   string
		tests []string
	}{
		{"db", []string{"TestStoreCloseWaitsForCancelledTransactionRollback", "TestGenerationTwoFreshConcurrentSingleOEXCLWinnerDoesNotDeleteWinner", "TestGenerationTwoBootstrapRecoveryRunsAfterCheckpointBeforeStoreReturn", "TestStartupCancellationAtEveryDatabaseStage", "TestStorageContractsCancelledTransactionRestoresForeignKeys"}},
		{"fatfish/engine", []string{"TestSharedLevelReplayLeavesCallerDataUnchanged", "TestReplayCancellationAtBoundedTick"}},
	} {
		for _, test := range item.tests {
			if excluded["github.com/waiting-here/NonbiriAPI/internal/"+item.pkg+"/"+test] {
				t.Fatalf("risk test excluded: %s", test)
			}
		}
	}
}

func TestDynamicShardsNeverAllocateEmptyWorkers(t *testing.T) {
	hints := timingHints{Shards: 16, DefaultPackageSeconds: 1, SplitPackages: []string{"example/db"}, SplitTestCounts: map[string]int{"example/db": 2}}
	for _, test := range []struct {
		catalog []catalogPackage
		want    int
	}{
		{nil, 0},
		{[]catalogPackage{{ImportPath: "example/one"}}, 1},
		{[]catalogPackage{{ImportPath: "example/db", Tests: []string{"TestOne", "TestTwo"}}}, 2},
	} {
		count := nonemptyShardCount(test.catalog, hints, 16)
		if count != test.want {
			t.Fatalf("count=%d want=%d", count, test.want)
		}
		if count == 0 {
			continue
		}
		caseHints := hints
		if count == 1 {
			caseHints.SplitPackages = nil
		}
		plans, err := buildPlans(test.catalog, caseHints, count)
		if err != nil {
			t.Fatal(err)
		}
		for _, plan := range plans {
			if len(executionGroups(plan, "1m", 4)) == 0 {
				t.Fatal("empty shard")
			}
		}
	}
}

func TestPreparedPlanRejectsChangedSourceAndMissingCoverage(t *testing.T) {
	catalog := []catalogPackage{{ImportPath: "example/db", Tests: []string{"TestOne", "TestTwo"}}}
	hints := timingHints{Shards: 2, DefaultPackageSeconds: 1, SplitPackages: []string{"example/db"}, SplitTestCounts: map[string]int{"example/db": 2}}
	plans, err := buildPlans(catalog, hints, 2)
	if err != nil {
		t.Fatal(err)
	}
	source := sourceIdentity{Commit: "commit", Tree: "tree", Go: "go1.26.6", Platform: "linux/amd64"}
	snapshot := preparedRun{Version: 1, Source: source, Catalog: catalog, Hints: hints, Plans: plans, Timeout: "1m", Workers: 4, Digest: planDigest(plans, "1m", 4)}
	if err := validatePrepared(snapshot, source, "1m", 4); err != nil {
		t.Fatal(err)
	}
	changed := source
	changed.Tree = "changed"
	if err := validatePrepared(snapshot, changed, "1m", 4); err == nil {
		t.Fatal("different source accepted")
	}
	if err := validatePrepared(snapshot, source, "1m", 2); err == nil {
		t.Fatal("different workers accepted")
	}
	snapshot.Plans = snapshot.Plans[:1]
	snapshot.Digest = planDigest(snapshot.Plans, "1m", 4)
	if err := validatePrepared(snapshot, source, "1m", 4); err == nil || !strings.Contains(err.Error(), "coverage") {
		t.Fatalf("missing shard accepted: %v", err)
	}
}
