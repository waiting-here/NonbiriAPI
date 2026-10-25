package requestattempt

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

// Fixed reasons are a privacy boundary shared by independent protocol parsers.
// Check their actual call sites so a new diagnostic cannot disappear silently.
func TestServerAuthoredRejectionReasonsAreRegistered(t *testing.T) {
	positions := token.NewFileSet()
	literal := func(expr ast.Expr) (string, bool) {
		value, ok := expr.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(value.Value)
		return text, err == nil
	}
	check := func(expr ast.Expr) {
		reason, ok := literal(expr)
		if !ok {
			return
		}
		if detail := NewDetail("request", reason); detail == nil || detail.Suggestion == "" {
			t.Errorf("%s: unregistered fixed rejection reason %q", positions.Position(expr.Pos()), reason)
		}
	}
	for _, root := range []string{"../connector", "../forward", "../charityrouting", "../claim"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(positions, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.CallExpr:
					name := ""
					switch fn := value.Fun.(type) {
					case *ast.Ident:
						name = fn.Name
					case *ast.SelectorExpr:
						name = fn.Sel.Name
					}
					index := -1
					switch name {
					case "NewDetail", "invalidField", "RejectionReason", "fieldRejection":
						index = 1
					case "describeInvalid":
						index = 2
					case "rejectionFailure":
						index = 3
					case "reject":
						if filepath.Base(filepath.Dir(path)) == "gateway" {
							index = 2
						}
					}
					if index >= 0 && index < len(value.Args) {
						check(value.Args[index])
					}
				case *ast.CompositeLit:
					typ, ok := value.Type.(*ast.SelectorExpr)
					if !ok || typ.Sel.Name != "RequestRejection" {
						break
					}
					for _, element := range value.Elts {
						pair, ok := element.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := pair.Key.(*ast.Ident)
						if ok && key.Name == "Reason" {
							check(pair.Value)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestEveryRegisteredDetailRoundTripsWithDerivedSuggestion(t *testing.T) {
	for reason := range rejectionReasonsAllowed {
		detail := NewDetail("request", reason)
		if got := DecodeDetail(EncodeDetail(detail)); got == nil || *got != *detail {
			t.Fatalf("%q did not round trip: %#v", reason, got)
		}
	}
	got := DecodeDetail(`{"field":"stream","reason":"expected a boolean","suggestion":"SECRET_VALUE"}`)
	if got == nil || strings.Contains(got.Suggestion, "SECRET") || got.Suggestion == "" {
		t.Fatal(got)
	}
}
