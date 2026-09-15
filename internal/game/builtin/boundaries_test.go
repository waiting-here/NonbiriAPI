package builtin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGameDependencyAndFinancialExecutionBoundaries(t *testing.T) {
	root := filepath.Clean("..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		owner := ""
		for _, id := range []string{"fishing", "linklink", "rps"} {
			if strings.HasPrefix(relative, id+"/") {
				owner = id
			}
		}
		ledgerAlias := ""
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			const base = "github.com/waiting-here/NonbiriAPI/internal/"
			if strings.HasPrefix(relative, "fishing/") && !strings.HasPrefix(relative, "fishing/runtime/") && importPath == "database/sql" {
				t.Errorf("pure rules import persistence: %s", relative)
			}
			if importPath == base+"ledger" {
				ledgerAlias = "ledger"
				if imp.Name != nil {
					ledgerAlias = imp.Name.Name
				}
			}
			if !strings.HasPrefix(importPath, base) {
				continue
			}
			dependency := strings.TrimPrefix(importPath, base)
			if !strings.Contains(relative, "/") && strings.HasPrefix(dependency, "game/") {
				t.Errorf("core imports concrete game: %s -> %s", relative, dependency)
			}
			if strings.HasPrefix(relative, "host/") && (strings.HasPrefix(dependency, "game/") || dependency == "forward" || dependency == "routing" || strings.HasPrefix(dependency, "connector")) {
				t.Errorf("host reverse dependency: %s -> %s", relative, dependency)
			}
			if owner != "" && strings.HasPrefix(dependency, "game/") && dependency != "game/host" && dependency != "game/finance" && dependency != "game/randomness" && dependency != "game/randomness/httpapi" && dependency != "game/"+owner && !strings.HasPrefix(dependency, "game/"+owner+"/") {
				t.Errorf("module crosses owner: %s -> %s", relative, dependency)
			}
			if strings.HasPrefix(relative, "randomness/") && (dependency == "ledger" || dependency == "forward" || dependency == "routing" || strings.HasPrefix(dependency, "connector") || strings.HasPrefix(dependency, "game/") && dependency != "game/randomness") {
				t.Errorf("random proof imports execution or concrete rules: %s -> %s", relative, dependency)
			}
			if owner != "" && (dependency == "forward" || dependency == "routing" || strings.HasPrefix(dependency, "connector")) {
				t.Errorf("game imports API execution: %s -> %s", relative, dependency)
			}
			if strings.HasPrefix(relative, "fishing/") && !strings.HasPrefix(relative, "fishing/runtime/") && dependency == "db" {
				t.Errorf("pure rules import persistence: %s", relative)
			}
		}
		if owner != "" {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				receiver, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				if receiver.Name == ledgerAlias {
					switch selector.Sel.Name {
					case "Apply", "Reserve", "ConsumeReserved", "ReleaseReserved", "CreateRPSQueueAccount", "CreateRPSSessionAccount", "NewFishingReserve", "NewFishingSettle", "NewFishingRelease", "NewLinkLinkEntry", "NewRPSQueueReserve", "NewRPSQueueRelease", "NewRPSSessionStart", "NewRPSRoundCut", "NewRPSTerminal":
						t.Errorf("module bypasses finance port: %s: %s", relative, selector.Sel.Name)
					}
				}
				if selector.Sel.Name == "ExecContext" && len(call.Args) > 1 {
					if literal, ok := call.Args[1].(*ast.BasicLit); ok && literal.Kind == token.STRING {
						query, _ := strconv.Unquote(literal.Value)
						normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
						for _, prefix := range []string{"insert into credit_", "update credit_", "delete from credit_", "update users ", "delete from users ", "insert into users "} {
							if strings.HasPrefix(normalized, prefix) {
								t.Errorf("module mutates protected data: %s", relative)
							}
						}
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
