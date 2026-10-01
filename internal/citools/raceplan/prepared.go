package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed exclusions.json
var exclusionJSON []byte

type exclusionGroup struct {
	Package   string   `json:"package"`
	Tests     []string `json:"tests"`
	Reason    string   `json:"reason"`
	Platforms []string `json:"platforms,omitempty"`
}

type raceExclusion struct {
	Package   string   `json:"package"`
	Test      string   `json:"test"`
	Reason    string   `json:"reason"`
	Platforms []string `json:"platforms,omitempty"`
}

// Exact reviewed names leave race; new names stay covered. Removed names force
// policy review instead of silently concealing catalog drift.
func filterRiskTests(catalog []catalogPackage, exclusions []raceExclusion, complete bool) ([]catalogPackage, []raceExclusion, error) {
	live := map[string]catalogPackage{}
	for _, pkg := range catalog {
		live[pkg.ImportPath] = pkg
	}
	removed := map[string]map[string]bool{}
	applied := []raceExclusion{}
	for _, item := range exclusions {
		applicable := len(item.Platforms) == 0
		for _, platform := range item.Platforms {
			applicable = applicable || platform == runtime.GOOS
		}
		if !applicable {
			continue
		}
		if item.Package == "" || !validTopLevelTestName(item.Test) || strings.TrimSpace(item.Reason) == "" {
			return nil, nil, errors.New("exclusion needs exact package/test and reason")
		}
		pkg, exists := live[item.Package]
		if !exists {
			if complete {
				return nil, nil, fmt.Errorf("obsolete exclusion package %s", item.Package)
			}
			continue
		}
		if pkg.TestMain {
			return nil, nil, fmt.Errorf("cannot partially exclude TestMain package %s", item.Package)
		}
		found := false
		for _, name := range pkg.Tests {
			found = found || name == item.Test
		}
		if !found {
			return nil, nil, fmt.Errorf("obsolete exclusion %s/%s", item.Package, item.Test)
		}
		if removed[item.Package] == nil {
			removed[item.Package] = map[string]bool{}
		}
		if removed[item.Package][item.Test] {
			return nil, nil, fmt.Errorf("duplicate exclusion %s/%s", item.Package, item.Test)
		}
		removed[item.Package][item.Test] = true
		applied = append(applied, item)
	}
	result := []catalogPackage{}
	for _, pkg := range catalog {
		if len(removed[pkg.ImportPath]) == 0 {
			result = append(result, pkg)
			continue
		}
		tests := []string{}
		for _, name := range pkg.Tests {
			if !removed[pkg.ImportPath][name] {
				tests = append(tests, name)
			}
		}
		if len(tests) > 0 {
			pkg.Tests = tests
			result = append(result, pkg)
		}
	}
	return result, applied, nil
}

func selectRiskTests(catalog []catalogPackage, hints timingHints, complete bool) ([]catalogPackage, timingHints, []raceExclusion, error) {
	var groups []exclusionGroup
	if err := json.Unmarshal(exclusionJSON, &groups); err != nil {
		return nil, hints, nil, err
	}
	exclusions := []raceExclusion{}
	for _, group := range groups {
		if len(group.Tests) == 0 {
			return nil, hints, nil, errors.New("empty exclusion group")
		}
		for _, test := range group.Tests {
			exclusions = append(exclusions, raceExclusion{Package: group.Package, Test: test, Reason: group.Reason, Platforms: group.Platforms})
		}
	}
	selected, applied, err := filterRiskTests(catalog, exclusions, complete)
	if err != nil {
		return nil, hints, nil, err
	}
	split := map[string]bool{}
	for _, name := range hints.SplitPackages {
		split[name] = true
	}
	for _, item := range applied {
		split[item.Package] = true
	}
	oldCounts, oldCaps, oldTests := hints.SplitTestCounts, hints.SplitGroupCaps, hints.TestSeconds
	hints.SplitPackages = nil
	hints.SplitTestCounts = map[string]int{}
	hints.SplitGroupCaps = map[string]int{}
	hints.TestSeconds = map[string]map[string]float64{}
	for _, pkg := range selected {
		if !split[pkg.ImportPath] {
			continue
		}
		hints.SplitPackages = append(hints.SplitPackages, pkg.ImportPath)
		hints.SplitTestCounts[pkg.ImportPath] = oldCounts[pkg.ImportPath]
		if cap, exists := oldCaps[pkg.ImportPath]; exists {
			hints.SplitGroupCaps[pkg.ImportPath] = cap
		}
		if tests, exists := oldTests[pkg.ImportPath]; exists {
			hints.TestSeconds[pkg.ImportPath] = tests
		}
		if hints.SplitTestCounts[pkg.ImportPath] <= 0 {
			hints.SplitTestCounts[pkg.ImportPath] = len(pkg.Tests)
		}
	}
	return selected, hints, applied, nil
}

func nonemptyShardCount(catalog []catalogPackage, hints timingHints, maximum int) int {
	split := map[string]bool{}
	for _, pkg := range hints.SplitPackages {
		split[pkg] = true
	}
	units := 0
	for _, pkg := range catalog {
		if split[pkg.ImportPath] {
			units += len(pkg.Tests)
		} else {
			units++
		}
	}
	return min(maximum, units)
}

type sourceIdentity struct {
	Commit   string
	Tree     string
	Go       string
	Platform string
}
type preparedRun struct {
	Version        int
	Source         sourceIdentity
	Catalog        []catalogPackage
	Hints          timingHints
	Plans          []shardPlan
	Excluded       []raceExclusion
	Timeout        string
	Workers        int
	Digest         string
	TemplateID     string
	TemplateSHA256 string
	TemplateBytes  int
}

func currentSource(goTool string) (sourceIdentity, error) {
	result := sourceIdentity{Platform: runtime.GOOS + "/" + runtime.GOARCH}
	for _, call := range []struct {
		target  *string
		command string
		args    []string
	}{
		{&result.Commit, "git", []string{"rev-parse", "HEAD"}},
		{&result.Tree, "git", []string{"rev-parse", "HEAD^{tree}"}},
		{&result.Go, goTool, []string{"version"}},
	} {
		body, err := exec.Command(call.command, call.args...).CombinedOutput()
		if err != nil {
			return result, fmt.Errorf("source identity: %w: %s", err, body)
		}
		*call.target = strings.TrimSpace(string(body))
	}
	body, err := exec.Command("git", "-c", "core.fsmonitor=false", "status", "--porcelain", "--untracked-files=no").CombinedOutput()
	if err != nil || len(body) > 0 {
		return result, errors.New("reusable race preparation requires a clean tracked source tree")
	}
	return result, nil
}
func prepareRun(goTool, dir string, catalog []catalogPackage, hints timingHints, plans []shardPlan, excluded []raceExclusion, timeout string, workers int, jobOutput, templateDir string, output io.Writer) (resultErr error) {
	source, err := currentSource(goTool)
	if err != nil {
		return err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("create prepared directory: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.RemoveAll(dir))
		}
	}()
	snapshot := preparedRun{Version: 1, Source: source, Catalog: catalog, Hints: hints, Plans: plans, Excluded: excluded, Timeout: timeout, Workers: workers, Digest: planDigest(plans, timeout, workers)}
	if len(plans) > 0 {
		var fixture runnerFixture
		if templateDir != "" {
			fixture, err = readSharedTemplate(goTool, templateDir)
		} else {
			fixture, err = prepareRunnerFixture(goTool, snapshot.Digest)
			if err == nil {
				defer func() { resultErr = errors.Join(resultErr, fixture.cleanup()) }()
			}
		}
		if err != nil {
			return err
		}
		image, err := os.ReadFile(fixture.path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, fixture.identity+".sqlite"), image, 0400); err != nil {
			return err
		}
		snapshot.TemplateID, snapshot.TemplateSHA256, snapshot.TemplateBytes = fixture.identity, fixture.sha256, fixture.size
		fmt.Fprintf(output, "raceplan: one template build=%s bytes=%d sha256=%s\n", fixture.buildTime, fixture.size, fixture.sha256)
	}
	body, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), append(body, '\n'), 0600); err != nil {
		return err
	}
	matrix := []string{}
	for index := range plans {
		matrix = append(matrix, fmt.Sprintf("%d/%d", index+1, len(plans)))
	}
	matrixJSON, _ := json.Marshal(matrix)
	if jobOutput != "" {
		file, err := os.OpenFile(jobOutput, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(file, "matrix=%s\ncount=%d\napplicable=%t\n", matrixJSON, len(plans), len(plans) > 0)
		err = errors.Join(err, file.Close())
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(output, "raceplan: catalog once; reviewed exclusions=%d nonempty shards=%d plan=%s\n", len(excluded), len(plans), snapshot.Digest)
	return nil
}
func validatePrepared(snapshot preparedRun, source sourceIdentity, timeout string, workers int) error {
	if snapshot.Version != 1 || snapshot.Source != source {
		return errors.New("prepared source/toolchain/inputs do not match")
	}
	if timeout != snapshot.Timeout || workers != snapshot.Workers {
		return errors.New("prepared timeout/workers do not match")
	}
	if snapshot.Digest != planDigest(snapshot.Plans, timeout, workers) {
		return errors.New("prepared plan digest mismatch")
	}
	split := map[string]struct{}{}
	for _, pkg := range snapshot.Hints.SplitPackages {
		split[pkg] = struct{}{}
	}
	if len(snapshot.Plans) == 0 {
		if len(snapshot.Catalog) > 0 {
			return errors.New("nonempty catalog lacks shards")
		}
		return nil
	}
	if err := validatePlans(snapshot.Catalog, split, snapshot.Plans); err != nil {
		return err
	}
	for _, plan := range snapshot.Plans {
		if len(executionGroups(plan, timeout, workers)) == 0 {
			return errors.New("prepared plan contains empty shard")
		}
	}
	return nil
}
func executePrepared(goTool, dir, shard, timeout string, workers int, output io.Writer) error {
	index, total, err := parseShardSpec(shard)
	if err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(dir, "plan.json"))
	if err != nil {
		return err
	}
	var snapshot preparedRun
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&snapshot)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = errors.New("prepared plan has trailing data")
		}
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	source, err := currentSource(goTool)
	if err != nil {
		return err
	}
	if err := validatePrepared(snapshot, source, timeout, workers); err != nil {
		return err
	}
	if total != len(snapshot.Plans) {
		return errors.New("matrix differs from prepared shards")
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, snapshot.TemplateID+".sqlite")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("prepared template is not regular")
	}
	image, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(image)
	if len(image) != snapshot.TemplateBytes || hex.EncodeToString(hash[:]) != snapshot.TemplateSHA256 {
		return errors.New("prepared template checksum/size mismatch")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(path, 0400); err != nil {
		return err
	}
	fixture := runnerFixture{path: path, identity: snapshot.TemplateID, sha256: snapshot.TemplateSHA256, size: len(image)}
	groups := executionGroups(snapshot.Plans[index-1], timeout, workers)
	fmt.Fprintf(output, "raceplan: reuse catalog/template shard=%s plan=%s groups=%d\n", shard, snapshot.Digest, len(groups))
	return executeGroups(groups, workers, func(group commandGroup, stream io.Writer) error {
		command := exec.Command(goTool, group.Args...)
		command.Env = fixture.childEnvironment(os.Environ())
		command.Stdout, command.Stderr = stream, stream
		return command.Run()
	}, output)
}
