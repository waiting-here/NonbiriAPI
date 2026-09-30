// Command riskplan selects a conservative verification closure from a Git diff.
// Live packages, test imports and platform sources define coverage. Uncertain
// inputs retain the complete verification path.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type packageInfo struct {
	ImportPath   string
	Dir          string
	Imports      []string
	TestImports  []string
	XTestImports []string
	EmbedFiles   []string
}

type verificationPlan struct {
	Mode         string   `json:"mode"`
	Reasons      []string `json:"reasons"`
	Changed      []string `json:"changed_files"`
	GoPackages   []string `json:"go_packages"`
	RacePackages []string `json:"race_packages"`
	Web          bool     `json:"web"`
	Upgrade      bool     `json:"upgrade"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "riskplan: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("riskplan", flag.ContinueOnError)
	mode := flags.String("mode", "routine", "routine or full verification")
	base := flags.String("base", "", "trusted ancestor commit; omitted means full verification")
	head := flags.String("head", "", "target commit; omitted includes working changes")
	goTool := flags.String("go", "go", "Go executable")
	githubOutput := flags.String("github-output", "", "append job outputs to this file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*mode != "routine" && *mode != "full") {
		return errors.New("expected -mode routine|full and no positional arguments")
	}
	packages, err := listPackages(*goTool)
	if err != nil {
		return err
	}
	changed, diffErr := changedFiles(*base, *head)
	plan := selectPlan(packages, changed, *mode, diffErr)
	if err := json.NewEncoder(output).Encode(plan); err != nil {
		return err
	}
	if *githubOutput != "" {
		return writeJobOutputs(*githubOutput, plan)
	}
	return nil
}

func commandOutput(name string, args ...string) ([]byte, error) {
	// Read scope from Git directly, independently of a host fsmonitor daemon.
	if name == "git" {
		args = append([]string{"-c", "core.fsmonitor=false"}, args...)
	}
	command := exec.Command(name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func changedFiles(base, head string) ([]string, error) {
	if base == "" {
		return nil, errors.New("no trusted diff baseline")
	}
	for _, ref := range []string{base, head} {
		if ref == "" {
			continue
		}
		if strings.HasPrefix(ref, "-") {
			return nil, errors.New("invalid commit reference")
		}
		if _, err := commandOutput("git", "rev-parse", "--verify", ref+"^{commit}"); err != nil {
			return nil, err
		}
	}
	ancestorHead := head
	if ancestorHead == "" {
		ancestorHead = "HEAD"
	}
	if _, err := commandOutput("git", "merge-base", "--is-ancestor", base, ancestorHead); err != nil {
		return nil, err
	}
	args := []string{"diff", "--no-renames", "--name-only", "-z", base}
	if head != "" {
		args = append(args, head)
	}
	args = append(args, "--")
	output, err := commandOutput("git", args...)
	if err != nil {
		return nil, err
	}
	if head == "" {
		untracked, err := commandOutput("git", "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		output = append(output, untracked...)
	}
	seen := map[string]bool{}
	for _, file := range bytes.Split(output, []byte{0}) {
		if len(file) != 0 {
			seen[filepath.ToSlash(string(file))] = true
		}
	}
	return sortedKeys(seen), nil
}

func listPackages(goTool string) ([]packageInfo, error) {
	// Both supported targets contribute imports; direct source parsing also
	// includes imports guarded by custom build tags and external test packages.
	merged := map[string]packageInfo{}
	for _, platform := range []string{"linux", "windows"} {
		command := exec.Command(goTool, "list", "-json", "./...")
		command.Env = platformEnvironment(os.Environ(), platform)
		output, err := command.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return nil, fmt.Errorf("go list %s: %w: %s", platform, err, strings.TrimSpace(string(exitErr.Stderr)))
			}
			return nil, fmt.Errorf("go list %s: %w", platform, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		for {
			var item packageInfo
			if err := decoder.Decode(&item); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, err
			}
			if item.ImportPath == "" || strings.Contains(item.ImportPath, "/node_modules/") || strings.Contains(filepath.ToSlash(item.Dir), "/node_modules/") {
				continue
			}
			prior := merged[item.ImportPath]
			item.EmbedFiles = append(item.EmbedFiles, prior.EmbedFiles...)
			item.Imports = append(append(append(item.Imports, item.TestImports...), item.XTestImports...), prior.Imports...)
			merged[item.ImportPath] = item
		}
	}
	packages := make([]packageInfo, 0, len(merged))
	for _, key := range sortedKeys(merged) {
		item := merged[key]
		files, err := os.ReadDir(item.Dir)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".go") {
				continue
			}
			source, err := parser.ParseFile(token.NewFileSet(), filepath.Join(item.Dir, file.Name()), nil, parser.ImportsOnly)
			if err != nil {
				return nil, err
			}
			for _, imported := range source.Imports {
				value, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return nil, err
				}
				item.Imports = append(item.Imports, value)
			}
		}
		packages = append(packages, item)
	}
	if len(packages) == 0 {
		return nil, errors.New("live Go package catalog is empty")
	}
	return packages, nil
}

func platformEnvironment(environment []string, platform string) []string {
	result := make([]string, 0, len(environment)+3)
	for _, value := range environment {
		if strings.HasPrefix(value, "GOOS=") || strings.HasPrefix(value, "GOARCH=") || strings.HasPrefix(value, "CGO_ENABLED=") {
			continue
		}
		result = append(result, value)
	}
	return append(result, "GOOS="+platform, "GOARCH=amd64", "CGO_ENABLED=0")
}

func selectPlan(packages []packageInfo, changed []string, mode string, diffErr error) verificationPlan {
	plan := verificationPlan{Mode: mode, Changed: changed, Reasons: []string{}, GoPackages: []string{}, RacePackages: []string{}}
	all := map[string]bool{}
	dirs := map[string]string{}
	productionInputs := map[string]bool{}
	reverse := map[string][]string{}
	root, rootErr := os.Getwd()
	for _, item := range packages {
		all[item.ImportPath] = true
		dir, err := filepath.Rel(root, item.Dir)
		if err == nil {
			dirs[filepath.ToSlash(dir)] = item.ImportPath
			for _, input := range item.EmbedFiles {
				productionInputs[filepath.ToSlash(filepath.Join(dir, input))] = true
			}
		}
		for _, imported := range item.Imports {
			reverse[imported] = append(reverse[imported], item.ImportPath)
		}
	}
	full := func(reason string) verificationPlan {
		plan.Mode = "full"
		plan.Reasons = append(plan.Reasons, reason)
		plan.GoPackages = sortedKeys(all)
		plan.RacePackages = append([]string(nil), plan.GoPackages...)
		plan.Web, plan.Upgrade = true, true
		return plan
	}
	if mode == "full" {
		return full("explicit full verification")
	}
	if diffErr != nil || rootErr != nil {
		return full("diff baseline or package graph is untrusted")
	}
	if len(changed) == 0 {
		return full("empty diff cannot establish a verification scope")
	}
	affected := map[string]bool{}
	race := false
	productionAffected := false
	for _, file := range changed {
		testSource := strings.HasSuffix(file, "_test.go")
		testInput := testSource || (strings.Contains(file, "/testdata/") && !productionInputs[file])
		if file == "go.mod" || file == "go.sum" || (filepath.Dir(file) == "." && strings.HasSuffix(file, ".go") && !testSource) || file == "web/package-lock.json" || file == "web/package.json" || strings.HasPrefix(file, "scripts/") || strings.HasPrefix(file, ".github/") || (!testInput && (strings.HasPrefix(file, "internal/db/") || strings.HasPrefix(file, "internal/lifecycle/"))) {
			return full("shared schema, lifecycle, root, dependency or gate input changed")
		}
		if strings.HasPrefix(file, "web/") {
			plan.Web = true
			continue
		}
		if file == "README.md" || file == "CHANGELOG.md" || file == "LICENSE" || strings.HasPrefix(file, "docs/") {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(file))
		packagePath := ""
		for {
			if dir == "." && (filepath.Dir(file) != "." || !strings.HasSuffix(file, ".go")) {
				break
			}
			if packagePath = dirs[dir]; packagePath != "" {
				break
			}
			if dir == "." {
				break
			}
			dir = filepath.ToSlash(filepath.Dir(dir))
		}
		if packagePath == "" {
			return full("changed input has no known verification owner")
		}
		affected[packagePath] = true
		if !testInput && !pureAlgorithmPackage(packagePath) {
			productionAffected = true
		}
		if !testInput && concurrentPackage(packagePath) {
			race = true
		}
		if strings.HasSuffix(file, ".go") {
			contents, err := os.ReadFile(file)
			if err != nil {
				return full("deleted or unreadable source needs complete verification")
			}
			if concurrentSource(contents) {
				race = true
			}
		}
	}
	queue := sortedKeys(affected)
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		for _, dependent := range reverse[item] {
			if !affected[dependent] {
				affected[dependent] = true
				queue = append(queue, dependent)
			}
		}
	}
	plan.GoPackages = sortedKeys(affected)
	if productionAffected {
		for _, pkg := range plan.GoPackages {
			if concurrentPackage(pkg) {
				race = true
			}
		}
	}
	if race {
		plan.RacePackages = append([]string(nil), plan.GoPackages...)
	}
	plan.Reasons = append(plan.Reasons, "live reverse dependency closure includes tests and platform sources")
	if !race {
		plan.Reasons = append(plan.Reasons, "no applicable Go concurrency risk")
	}
	return plan
}

func pureAlgorithmPackage(path string) bool {
	for _, name := range []string{"lakenotes/rules", "fatfish/engine", "game/bidding/engine", "game/blackjack/engine", "game/likes/engine"} {
		if strings.HasSuffix(path, "/internal/"+name) {
			return true
		}
	}
	return false
}

func concurrentPackage(packagePath string) bool {
	if pureAlgorithmPackage(packagePath) {
		return false
	}
	if strings.Contains(packagePath, "/internal/game/") && !strings.HasSuffix(packagePath, "/engine") && !strings.HasSuffix(packagePath, "/randomness") {
		return true
	}

	for _, name := range []string{"auth", "authz", "secret", "egress", "claim", "charity", "charityrouting", "donation", "forward", "ledger", "idempotency", "lifecycle", "resources", "fatfish", "lakenotes", "limitedactivities", "riskaudit", "clientguard", "observability"} {
		prefix := "/internal/" + name
		if strings.HasSuffix(packagePath, prefix) || strings.Contains(packagePath, prefix+"/") && !strings.HasSuffix(packagePath, "/engine") && !strings.HasSuffix(packagePath, "/rules") {
			return true
		}
	}
	return false
}

func concurrentSource(source []byte) bool {
	parsed, err := parser.ParseFile(token.NewFileSet(), "source.go", source, 0)
	if err != nil {
		return true
	}
	for _, imported := range parsed.Imports {
		if imported.Path.Value == "\"sync\"" || imported.Path.Value == "\"sync/atomic\"" {
			return true
		}
	}
	concurrent := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.GoStmt, *ast.ChanType:
			concurrent = true
		case *ast.CallExpr:
			if selector, ok := value.Fun.(*ast.SelectorExpr); ok {
				switch selector.Sel.Name {
				case "WithCancel", "WithCancelCause", "WithDeadline", "WithDeadlineCause", "WithTimeout", "WithTimeoutCause", "AfterFunc":
					concurrent = true
				}
			}
			if name, ok := value.Fun.(*ast.Ident); ok && name.Name == "close" {
				concurrent = true
			}
		}
		return !concurrent
	})
	return concurrent
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeJobOutputs(path string, plan verificationPlan) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "mode=%s\ngo=%t\nrace=%t\nweb=%t\nupgrade=%t\ngo_packages=%s\nrace_packages=%s\n", plan.Mode, len(plan.GoPackages) > 0, len(plan.RacePackages) > 0, plan.Web, plan.Upgrade, strings.Join(plan.GoPackages, " "), strings.Join(plan.RacePackages, " "))
	return err
}
