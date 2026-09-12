// Command raceplan builds and executes one deterministic shard of the complete
// Go race-test catalog. Timing hints affect balance only; live go list and
// go test -list output remain the coverage authority.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const testListPattern = `^(Test|Fuzz|Example)`

type timingHints struct {
	Version               int                           `json:"version"`
	Shards                int                           `json:"shards"`
	DefaultPackageSeconds float64                       `json:"default_package_seconds"`
	SplitPackages         []string                      `json:"split_packages"`
	SplitTestCounts       map[string]int                `json:"split_test_counts"`
	TestSeconds           map[string]map[string]float64 `json:"test_seconds,omitempty"`
	PackageSeconds        map[string]float64            `json:"package_seconds"`
}

type listedPackage struct {
	ImportPath   string
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
}

type catalogPackage struct {
	ImportPath string
	Tests      []string
	TestMain   bool
}

type planUnit struct {
	Package string
	Test    string
	Weight  float64
}

type shardPlan struct {
	Index               int
	EstimatedSeconds    float64
	WholePackages       []string
	WholePackageWeights map[string]float64
	SplitTests          map[string][]string
	SplitTestWeights    map[string]map[string]float64
}

type commandGroup struct {
	Label  string
	Args   []string
	Weight float64
}

func main() {
	if err := runCLI(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "raceplan: %v\n", err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdout, stderr io.Writer) error {
	goDefault := os.Getenv("GO")
	if goDefault == "" {
		goDefault = "go"
	}
	flags := flag.NewFlagSet("raceplan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	goTool := flags.String("go", goDefault, "Go executable")
	shardSpec := flags.String("shard", "", "one-based shard in N/TOTAL form")
	timeout := flags.String("timeout", "30m", "per-command go test timeout")
	hintsPath := flags.String("hints", "scripts/race-timings.json", "timing hints JSON")
	planOnly := flags.Bool("plan-only", false, "print the complete deterministic plan without running tests")
	workers := flags.Int("workers", 1, "maximum concurrent test commands")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if !*planOnly && *shardSpec == "" {
		return errors.New("shard is required unless -plan-only is set")
	}
	if *workers < 1 {
		return fmt.Errorf("invalid workers %d; want a positive value", *workers)
	}
	index, total := 0, 0
	if *shardSpec != "" {
		var err error
		index, total, err = parseShardSpec(*shardSpec)
		if err != nil {
			return err
		}
	}
	duration, err := time.ParseDuration(*timeout)
	if err != nil || duration <= 0 {
		return fmt.Errorf("invalid timeout %q", *timeout)
	}
	hints, err := loadTimingHints(*hintsPath)
	if err != nil {
		return err
	}
	if total == 0 {
		total = hints.Shards
	} else if hints.Shards != total {
		return fmt.Errorf("shard total %d does not match timing hints total %d", total, hints.Shards)
	}
	listed, err := listPackages(*goTool)
	if err != nil {
		return err
	}
	catalog, err := buildCatalog(*goTool, listed, hints)
	if err != nil {
		return err
	}
	plans, err := buildPlans(catalog, hints, total)
	if err != nil {
		return err
	}
	digest := planDigest(plans)
	if *planOnly {
		printPlans(stdout, plans, digest, *timeout, *workers)
		return nil
	}
	selected := plans[index-1]
	testCount := 0
	for _, tests := range selected.SplitTests {
		testCount += len(tests)
	}
	fmt.Fprintf(stdout, "raceplan: shard %d/%d plan=%s estimated=%.1fs whole_packages=%d split_tests=%d\n",
		index, total, digest, selected.EstimatedSeconds, len(selected.WholePackages), testCount)
	groups := executionGroups(selected, *timeout, *workers)
	return executeGroups(groups, *workers, func(group commandGroup, commandOutput io.Writer) error {
		command := exec.Command(*goTool, group.Args...)
		command.Stdout = commandOutput
		command.Stderr = commandOutput
		return command.Run()
	}, stdout)
}

func parseShardSpec(value string) (int, int, error) {
	var index, total int
	if _, err := fmt.Sscanf(value, "%d/%d", &index, &total); err != nil || fmt.Sprintf("%d/%d", index, total) != value {
		return 0, 0, fmt.Errorf("invalid shard %q; want N/TOTAL", value)
	}
	if total < 1 || index < 1 || index > total {
		return 0, 0, fmt.Errorf("invalid shard %q; N must be between 1 and TOTAL", value)
	}
	return index, total, nil
}

func loadTimingHints(path string) (timingHints, error) {
	file, err := os.Open(path)
	if err != nil {
		return timingHints{}, fmt.Errorf("open timing hints: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var hints timingHints
	if err := decoder.Decode(&hints); err != nil {
		return timingHints{}, fmt.Errorf("decode timing hints: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return timingHints{}, errors.New("decode timing hints: multiple JSON values")
		}
		return timingHints{}, fmt.Errorf("decode timing hints trailing data: %w", err)
	}
	if hints.Version != 1 || hints.Shards < 1 || hints.DefaultPackageSeconds <= 0 {
		return timingHints{}, errors.New("timing hints have invalid version, shard count, or default weight")
	}
	seenSplit := make(map[string]struct{}, len(hints.SplitPackages))
	for _, packagePath := range hints.SplitPackages {
		if packagePath == "" {
			return timingHints{}, errors.New("timing hints contain an empty split package")
		}
		if _, duplicate := seenSplit[packagePath]; duplicate {
			return timingHints{}, fmt.Errorf("timing hints repeat split package %s", packagePath)
		}
		seenSplit[packagePath] = struct{}{}
		if hints.SplitTestCounts[packagePath] <= 0 {
			return timingHints{}, fmt.Errorf("timing hints lack a positive test count for split package %s", packagePath)
		}
		if hints.PackageSeconds[packagePath] <= 0 {
			return timingHints{}, fmt.Errorf("timing hints lack a positive weight for split package %s", packagePath)
		}
	}
	for packagePath, count := range hints.SplitTestCounts {
		if _, exists := seenSplit[packagePath]; !exists || count <= 0 {
			return timingHints{}, fmt.Errorf("timing hints contain invalid split test count %q=%d", packagePath, count)
		}
	}
	for packagePath, seconds := range hints.PackageSeconds {
		if packagePath == "" || seconds <= 0 {
			return timingHints{}, fmt.Errorf("timing hints contain invalid package weight %q=%v", packagePath, seconds)
		}
	}
	for packagePath, tests := range hints.TestSeconds {
		if packagePath == "" {
			return timingHints{}, errors.New("timing hints contain an empty test-weight package")
		}
		if _, split := seenSplit[packagePath]; !split {
			return timingHints{}, fmt.Errorf("timing hints contain test weights for unsplit package %s", packagePath)
		}
		if len(tests) == 0 {
			return timingHints{}, fmt.Errorf("timing hints contain no test weights for %s", packagePath)
		}
		for testName, seconds := range tests {
			if !validTopLevelTestName(testName) {
				return timingHints{}, fmt.Errorf("timing hints contain invalid test name %q for %s", testName, packagePath)
			}
			if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
				return timingHints{}, fmt.Errorf("timing hints contain invalid test weight %s/%s=%v", packagePath, testName, seconds)
			}
		}
	}
	return hints, nil
}

func listPackages(goTool string) ([]listedPackage, error) {
	command := exec.Command(goTool, "list", "-race", "-json", "./...")
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("go list: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	packages := make([]listedPackage, 0, 64)
	for {
		var item listedPackage
		if err := decoder.Decode(&item); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		if item.ImportPath == "" || strings.Contains(item.ImportPath, "/node_modules/") ||
			strings.Contains(filepath.ToSlash(item.Dir), "/node_modules/") {
			continue
		}
		packages = append(packages, item)
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].ImportPath < packages[j].ImportPath })
	return packages, nil
}

func buildCatalog(goTool string, listed []listedPackage, hints timingHints) ([]catalogPackage, error) {
	split := make(map[string]struct{}, len(hints.SplitPackages))
	for _, packagePath := range hints.SplitPackages {
		split[packagePath] = struct{}{}
	}
	catalog := make([]catalogPackage, 0, len(listed))
	seen := make(map[string]struct{}, len(listed))
	for _, item := range listed {
		if _, duplicate := seen[item.ImportPath]; duplicate {
			return nil, fmt.Errorf("go list repeated package %s", item.ImportPath)
		}
		seen[item.ImportPath] = struct{}{}
		entry := catalogPackage{ImportPath: item.ImportPath}
		if _, shouldSplit := split[item.ImportPath]; shouldSplit {
			hasMain, err := packageHasTestMain(item)
			if err != nil {
				return nil, err
			}
			if hasMain {
				return nil, fmt.Errorf("split package %s declares TestMain", item.ImportPath)
			}
			entry.Tests, err = listTopLevelTests(goTool, item.ImportPath)
			if err != nil {
				return nil, err
			}
		}
		catalog = append(catalog, entry)
	}
	for packagePath := range split {
		if _, exists := seen[packagePath]; !exists {
			return nil, fmt.Errorf("configured split package %s is absent from go list", packagePath)
		}
	}
	return catalog, nil
}

func packageHasTestMain(item listedPackage) (bool, error) {
	files := append(append([]string(nil), item.TestGoFiles...), item.XTestGoFiles...)
	for _, name := range files {
		path := filepath.Join(item.Dir, name)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return false, fmt.Errorf("parse %s while checking TestMain: %w", path, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv == nil && function.Name.Name == "TestMain" {
				return true, nil
			}
		}
	}
	return false, nil
}

func listTopLevelTests(goTool, packagePath string) ([]string, error) {
	command := exec.Command(goTool, "test", "-race", "-list", testListPattern, packagePath)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list tests for %s: %w: %s", packagePath, err, strings.TrimSpace(string(output)))
	}
	seen := make(map[string]struct{})
	for _, line := range strings.Split(string(output), "\n") {
		name := strings.TrimSpace(line)
		if !validTopLevelTestName(name) {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("go test -list repeated %s/%s", packagePath, name)
		}
		seen[name] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("configured split package %s has no listed tests", packagePath)
	}
	tests := make([]string, 0, len(seen))
	for name := range seen {
		tests = append(tests, name)
	}
	sort.Strings(tests)
	return tests, nil
}

func validTopLevelTestName(name string) bool {
	if !token.IsIdentifier(name) {
		return false
	}
	for _, prefix := range []string{"Test", "Fuzz", "Example"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if len(name) == len(prefix) {
			return true
		}
		next, _ := utf8.DecodeRuneInString(name[len(prefix):])
		return !unicode.IsLower(next)
	}
	return false
}

func buildPlans(catalog []catalogPackage, hints timingHints, total int) ([]shardPlan, error) {
	if total < 1 {
		return nil, errors.New("shard count must be positive")
	}
	split := make(map[string]struct{}, len(hints.SplitPackages))
	for _, packagePath := range hints.SplitPackages {
		split[packagePath] = struct{}{}
	}
	if len(catalog) == 0 {
		return nil, errors.New("catalog is empty")
	}
	seenPackages := make(map[string]struct{}, len(catalog))
	units := make([]planUnit, 0, len(catalog))
	for _, item := range catalog {
		if item.ImportPath == "" {
			return nil, errors.New("catalog contains an empty package path")
		}
		if _, duplicate := seenPackages[item.ImportPath]; duplicate {
			return nil, fmt.Errorf("catalog repeats package %s", item.ImportPath)
		}
		seenPackages[item.ImportPath] = struct{}{}
		packageWeight := hints.PackageSeconds[item.ImportPath]
		if packageWeight <= 0 {
			packageWeight = hints.DefaultPackageSeconds
		}
		if _, shouldSplit := split[item.ImportPath]; !shouldSplit {
			units = append(units, planUnit{Package: item.ImportPath, Weight: packageWeight})
			continue
		}
		if item.TestMain {
			return nil, fmt.Errorf("split package %s declares TestMain", item.ImportPath)
		}
		if len(item.Tests) == 0 {
			return nil, fmt.Errorf("split package %s has no tests", item.ImportPath)
		}
		seenTests := make(map[string]struct{}, len(item.Tests))
		baselineCount := hints.SplitTestCounts[item.ImportPath]
		if baselineCount <= 0 {
			return nil, fmt.Errorf("split package %s has no positive baseline test count", item.ImportPath)
		}
		// Keep the prior per-test average when the live catalog grows. New tests
		// therefore add conservative weight instead of diluting the package total.
		for _, testName := range item.Tests {
			if !validTopLevelTestName(testName) {
				return nil, fmt.Errorf("split package %s has invalid test name %q", item.ImportPath, testName)
			}
			if _, duplicate := seenTests[testName]; duplicate {
				return nil, fmt.Errorf("split package %s repeats test %s", item.ImportPath, testName)
			}
			seenTests[testName] = struct{}{}
			testWeight := packageWeight / float64(baselineCount)
			if measured, ok := hints.TestSeconds[item.ImportPath][testName]; ok {
				testWeight = measured
			}
			units = append(units, planUnit{Package: item.ImportPath, Test: testName, Weight: testWeight})
		}
	}
	for packagePath := range split {
		if _, exists := seenPackages[packagePath]; !exists {
			return nil, fmt.Errorf("split package %s is absent from catalog", packagePath)
		}
	}
	sort.Slice(units, func(i, j int) bool {
		if units[i].Weight != units[j].Weight {
			return units[i].Weight > units[j].Weight
		}
		if units[i].Package != units[j].Package {
			return units[i].Package < units[j].Package
		}
		return units[i].Test < units[j].Test
	})
	plans := make([]shardPlan, total)
	for index := range plans {
		plans[index] = shardPlan{
			Index:               index + 1,
			WholePackageWeights: make(map[string]float64),
			SplitTests:          make(map[string][]string),
			SplitTestWeights:    make(map[string]map[string]float64),
		}
	}
	for _, unit := range units {
		target := 0
		for index := 1; index < len(plans); index++ {
			if plans[index].EstimatedSeconds < plans[target].EstimatedSeconds {
				target = index
			}
		}
		plans[target].EstimatedSeconds += unit.Weight
		if unit.Test == "" {
			plans[target].WholePackages = append(plans[target].WholePackages, unit.Package)
			plans[target].WholePackageWeights[unit.Package] = unit.Weight
		} else {
			plans[target].SplitTests[unit.Package] = append(plans[target].SplitTests[unit.Package], unit.Test)
			if plans[target].SplitTestWeights[unit.Package] == nil {
				plans[target].SplitTestWeights[unit.Package] = make(map[string]float64)
			}
			plans[target].SplitTestWeights[unit.Package][unit.Test] = unit.Weight
		}
	}
	for index := range plans {
		sort.Strings(plans[index].WholePackages)
		for packagePath := range plans[index].SplitTests {
			sort.Strings(plans[index].SplitTests[packagePath])
		}
	}
	if err := validatePlans(catalog, split, plans); err != nil {
		return nil, err
	}
	return plans, nil
}

func validatePlans(catalog []catalogPackage, split map[string]struct{}, plans []shardPlan) error {
	expectedWhole := make(map[string]struct{})
	expectedTests := make(map[string]map[string]struct{})
	for _, item := range catalog {
		if _, shouldSplit := split[item.ImportPath]; !shouldSplit {
			expectedWhole[item.ImportPath] = struct{}{}
			continue
		}
		expectedTests[item.ImportPath] = make(map[string]struct{}, len(item.Tests))
		for _, testName := range item.Tests {
			expectedTests[item.ImportPath][testName] = struct{}{}
		}
	}
	wholeCounts := make(map[string]int)
	testCounts := make(map[string]map[string]int)
	for index, plan := range plans {
		if plan.Index != index+1 {
			return fmt.Errorf("shard at offset %d has index %d", index, plan.Index)
		}
		for _, packagePath := range plan.WholePackages {
			if _, exists := expectedWhole[packagePath]; !exists {
				return fmt.Errorf("plan contains unexpected whole package %s", packagePath)
			}
			wholeCounts[packagePath]++
		}
		for packagePath, tests := range plan.SplitTests {
			expected, exists := expectedTests[packagePath]
			if !exists {
				return fmt.Errorf("plan contains unexpected split package %s", packagePath)
			}
			if testCounts[packagePath] == nil {
				testCounts[packagePath] = make(map[string]int)
			}
			for _, testName := range tests {
				if _, exists := expected[testName]; !exists {
					return fmt.Errorf("plan contains unexpected split test %s/%s", packagePath, testName)
				}
				testCounts[packagePath][testName]++
			}
		}
	}
	for _, item := range catalog {
		if _, shouldSplit := split[item.ImportPath]; !shouldSplit {
			if wholeCounts[item.ImportPath] != 1 || len(testCounts[item.ImportPath]) != 0 {
				return fmt.Errorf("whole package %s coverage is not exactly once", item.ImportPath)
			}
			continue
		}
		if wholeCounts[item.ImportPath] != 0 || len(testCounts[item.ImportPath]) != len(item.Tests) {
			return fmt.Errorf("split package %s coverage count mismatch", item.ImportPath)
		}
		for _, testName := range item.Tests {
			if testCounts[item.ImportPath][testName] != 1 {
				return fmt.Errorf("split test %s/%s coverage is not exactly once", item.ImportPath, testName)
			}
		}
	}
	return nil
}

func planDigest(plans []shardPlan) string {
	hash := sha256.New()
	for _, plan := range plans {
		fmt.Fprintf(hash, "shard:%d\n", plan.Index)
		for _, packagePath := range plan.WholePackages {
			fmt.Fprintf(hash, "package:%s\n", packagePath)
		}
		packages := make([]string, 0, len(plan.SplitTests))
		for packagePath := range plan.SplitTests {
			packages = append(packages, packagePath)
		}
		sort.Strings(packages)
		for _, packagePath := range packages {
			for _, testName := range plan.SplitTests[packagePath] {
				fmt.Fprintf(hash, "test:%s:%s\n", packagePath, testName)
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

func printPlans(output io.Writer, plans []shardPlan, digest, timeout string, workers int) {
	fmt.Fprintf(output, "raceplan: plan=%s shards=%d\n", digest, len(plans))
	for _, plan := range plans {
		testCount := 0
		for _, tests := range plan.SplitTests {
			testCount += len(tests)
		}
		fmt.Fprintf(output, "raceplan: shard %d/%d estimated=%.1fs whole_packages=%d split_tests=%d\n",
			plan.Index, len(plans), plan.EstimatedSeconds, len(plan.WholePackages), testCount)
		for _, packagePath := range plan.WholePackages {
			fmt.Fprintf(output, "raceplan: shard %d whole %s\n", plan.Index, packagePath)
		}
		packages := make([]string, 0, len(plan.SplitTests))
		for packagePath := range plan.SplitTests {
			packages = append(packages, packagePath)
		}
		sort.Strings(packages)
		for _, packagePath := range packages {
			fmt.Fprintf(output, "raceplan: shard %d split %s tests=%s\n", plan.Index, packagePath, strings.Join(plan.SplitTests[packagePath], ","))
		}
		for _, group := range executionGroups(plan, timeout, workers) {
			fmt.Fprintf(output, "raceplan: shard %d group %s weight=%.1f args=%q\n", plan.Index, group.Label, group.Weight, group.Args)
		}
	}
}

func executionGroups(plan shardPlan, timeout string, workers int) []commandGroup {
	base := []string{"test", "-race", "-count=1", "-timeout=" + timeout, "-p=1", "-v"}
	groups := make([]commandGroup, 0, len(plan.WholePackages)+len(plan.SplitTests))
	wholePackages := append([]string(nil), plan.WholePackages...)
	sort.Strings(wholePackages)
	for _, packagePath := range wholePackages {
		groups = append(groups, commandGroup{
			Label:  packagePath,
			Args:   append(append([]string(nil), base...), packagePath),
			Weight: plan.WholePackageWeights[packagePath],
		})
	}
	packages := make([]string, 0, len(plan.SplitTests))
	for packagePath := range plan.SplitTests {
		packages = append(packages, packagePath)
	}
	sort.Strings(packages)
	for _, packagePath := range packages {
		tests := plan.SplitTests[packagePath]
		groupCount := workers
		if len(tests) < groupCount {
			groupCount = len(tests)
		}
		buckets := make([][]string, groupCount)
		weights := make([]float64, groupCount)
		ordered := append([]string(nil), tests...)
		sort.SliceStable(ordered, func(i, j int) bool {
			left := plan.SplitTestWeights[packagePath][ordered[i]]
			right := plan.SplitTestWeights[packagePath][ordered[j]]
			if left != right {
				return left > right
			}
			return ordered[i] < ordered[j]
		})
		for _, testName := range ordered {
			weight := plan.SplitTestWeights[packagePath][testName]
			target := 0
			for index := 1; index < len(weights); index++ {
				if weights[index] < weights[target] {
					target = index
				}
			}
			buckets[target] = append(buckets[target], testName)
			weights[target] += weight
		}
		for index, bucket := range buckets {
			sort.Strings(bucket)
			parts := make([]string, len(bucket))
			for partIndex, testName := range bucket {
				parts[partIndex] = regexp.QuoteMeta(testName)
			}
			pattern := "^(" + strings.Join(parts, "|") + ")$"
			label := fmt.Sprintf("%s group %d/%d", packagePath, index+1, groupCount)
			args := append(append([]string(nil), base...), "-run", pattern, packagePath)
			groups = append(groups, commandGroup{Label: label, Args: args, Weight: weights[index]})
		}
	}
	return groups
}

func executeGroups(groups []commandGroup, workers int, runner func(commandGroup, io.Writer) error, output io.Writer) error {
	if workers < 1 {
		return fmt.Errorf("invalid workers %d; want a positive value", workers)
	}
	order := make([]int, len(groups))
	for index := range groups {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool {
		return groups[order[i]].Weight > groups[order[j]].Weight
	})
	workerCount := workers
	if len(groups) < workerCount {
		workerCount = len(groups)
	}
	jobs := make(chan int, workerCount)
	errorsByIndex := make([]error, len(groups))
	var outputMu sync.Mutex
	worker := func() {
		for index := range jobs {
			group := groups[index]
			started := time.Now()
			outputMu.Lock()
			fmt.Fprintf(output, "raceplan: running %s\n", group.Label)
			outputMu.Unlock()
			var commandOutput bytes.Buffer
			err := runner(group, &commandOutput)
			outputMu.Lock()
			if commandOutput.Len() > 0 {
				fmt.Fprintf(output, "raceplan: output %s\n", group.Label)
				_, _ = output.Write(commandOutput.Bytes())
				contents := commandOutput.Bytes()
				if contents[len(contents)-1] != '\n' {
					fmt.Fprintln(output)
				}
			}
			fmt.Fprintf(output, "raceplan: finished %s in %s\n", group.Label, time.Since(started).Round(time.Millisecond))
			outputMu.Unlock()
			if err != nil {
				errorsByIndex[index] = err
			}
		}
	}
	var wg sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		wg.Add(1)
		go func() { defer wg.Done(); worker() }()
	}
	for _, index := range order {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	failures := make([]string, 0)
	for index, err := range errorsByIndex {
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", groups[index].Label, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d race command(s) failed: %s", len(failures), strings.Join(failures, "; "))
	}
	return nil
}
