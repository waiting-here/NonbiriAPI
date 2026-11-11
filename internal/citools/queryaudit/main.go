package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Call struct {
	Expression string
	Name       string
	Package    string
}
type Query struct {
	File       string
	Line       int
	Function   string
	Method     string
	Receiver   string
	Expression string
	SQL        string
	Flags      []string
}
type Route struct {
	File     string
	Line     int
	Function string
	Method   string
	Path     string
	Handler  string
	Kind     string
}
type Function struct {
	File      string
	Line      int
	Package   string
	Name      string
	Receiver  string
	Calls     []Call
	Queries   []int
	Deadlines []string
}
type Inventory struct {
	Root      string
	Functions []Function
	Queries   []Query
	Routes    []Route
}
type Source struct {
	path, pkg string
	node      *ast.File
	imports   map[string]string
}

func render(n ast.Node) string {
	var b bytes.Buffer
	if n != nil {
		_ = format.Node(&b, token.NewFileSet(), n)
	}
	return b.String()
}
func unquote(v string) string {
	s, e := strconv.Unquote(v)
	if e != nil {
		return v
	}
	return s
}
func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: queryaudit REPOSITORY_ROOT OUTPUT.json")
		os.Exit(2)
	}
	root, out := os.Args[1], os.Args[2]
	set := token.NewFileSet()
	var sources []Source
	globals := map[string]ast.Expr{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == "citools" || d.Name() == "sqliteadapter" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		node, e := parser.ParseFile(set, path, nil, 0)
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		pkg := filepath.ToSlash(filepath.Dir(rel))
		imports := map[string]string{}
		for _, i := range node.Imports {
			p := unquote(i.Path.Value)
			name := filepath.Base(p)
			if i.Name != nil {
				name = i.Name.Name
			}
			imports[name] = strings.TrimPrefix(p, "github.com/waiting-here/NonbiriAPI/")
		}
		for _, decl := range node.Decls {
			if g, ok := decl.(*ast.GenDecl); ok {
				for _, spec := range g.Specs {
					if v, ok := spec.(*ast.ValueSpec); ok {
						for n, id := range v.Names {
							if n < len(v.Values) {
								globals[pkg+"."+id.Name] = v.Values[n]
							}
						}
					}
				}
			}
		}
		sources = append(sources, Source{rel, pkg, node, imports})
		return nil
	})
	if err != nil {
		panic(err)
	}
	result := Inventory{Root: root}
	var expression func(ast.Expr, string, map[string]bool, int) string
	expression = func(e ast.Expr, pkg string, seen map[string]bool, depth int) string {
		if e == nil {
			return ""
		}
		if depth > 20 {
			return "{{depth-limit}}"
		}
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				return unquote(v.Value)
			}
		case *ast.BinaryExpr:
			if v.Op == token.ADD {
				return expression(v.X, pkg, seen, depth+1) + expression(v.Y, pkg, seen, depth+1)
			}
		case *ast.ParenExpr:
			return expression(v.X, pkg, seen, depth+1)
		case *ast.Ident:
			key := pkg + "." + v.Name
			if !seen[key] {
				seen[key] = true
				defer delete(seen, key)
				if x, ok := globals[key]; ok {
					return expression(x, pkg, seen, depth+1)
				}
				if v.Obj != nil {
					switch d := v.Obj.Decl.(type) {
					case *ast.AssignStmt:
						for i, l := range d.Lhs {
							if id, ok := l.(*ast.Ident); ok && id.Name == v.Name && i < len(d.Rhs) {
								return expression(d.Rhs[i], pkg, seen, depth+1)
							}
						}
					case *ast.ValueSpec:
						for i, id := range d.Names {
							if id.Name == v.Name && i < len(d.Values) {
								return expression(d.Values[i], pkg, seen, depth+1)
							}
						}
					}
				}
			}
		case *ast.CallExpr:
			var parts []string
			for _, a := range v.Args {
				parts = append(parts, expression(a, pkg, seen, depth+1))
			}
			return "{{" + render(v.Fun) + "}}(" + strings.Join(parts, " | ") + ")"
		}
		return "{{" + render(e) + "}}"
	}
	keywords := map[string]*regexp.Regexp{}
	for _, flag := range []string{"COUNT", "SUM", "GROUP BY", "DISTINCT", "ORDER BY", "OFFSET", "LIMIT", "MATERIALIZED", "EXISTS", "LIKE", "INSTR", "JSON_", "UNION", "ROW_NUMBER"} {
		keywords[flag] = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(flag))
	}
	for _, src := range sources {
		for _, decl := range src.node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			recv := ""
			if fn.Recv != nil {
				recv = render(fn.Recv.List[0].Type)
			}
			name := src.pkg + "."
			if recv != "" {
				name += recv + "."
			}
			name += fn.Name.Name
			f := Function{File: src.path, Line: set.Position(fn.Pos()).Line, Package: src.pkg, Name: fn.Name.Name, Receiver: recv}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					leaf, pkg := render(c.Fun), src.pkg
					if s, ok := c.Fun.(*ast.SelectorExpr); ok {
						leaf = s.Sel.Name
						if id, ok := s.X.(*ast.Ident); ok {
							if p, ok := src.imports[id.Name]; ok {
								pkg = p
							}
						}
					}
					f.Calls = append(f.Calls, Call{render(c.Fun), leaf, pkg})
					if leaf == "WithTimeout" || leaf == "WithDeadline" {
						f.Deadlines = append(f.Deadlines, render(c))
					}
					arg := -1
					switch leaf {
					case "QueryContext", "QueryRowContext", "PrepareContext":
						arg = 1
					case "Query", "QueryRow", "Prepare":
						arg = 0
					}
					if arg >= 0 && len(c.Args) > arg {
						receiver := ""
						if s, ok := c.Fun.(*ast.SelectorExpr); ok {
							receiver = render(s.X)
						}
						sql := expression(c.Args[arg], src.pkg, map[string]bool{}, 0)
						q := Query{File: src.path, Line: set.Position(c.Pos()).Line, Function: name, Method: leaf, Receiver: receiver, Expression: render(c.Args[arg]), SQL: sql}
						for flag, re := range keywords {
							if re.MatchString(sql) {
								q.Flags = append(q.Flags, flag)
							}
						}
						sort.Strings(q.Flags)
						f.Queries = append(f.Queries, len(result.Queries))
						result.Queries = append(result.Queries, q)
					}
					if strings.Contains(leaf, "Register") && strings.HasSuffix(leaf, "Route") && len(c.Args) >= 3 {
						result.Routes = append(result.Routes, Route{src.path, set.Position(c.Pos()).Line, name, render(c.Args[0]), expression(c.Args[1], src.pkg, map[string]bool{}, 0), render(c.Args[2]), "registration"})
					}
				}
				if c, ok := n.(*ast.CompositeLit); ok && len(c.Elts) >= 3 {
					first, ok := c.Elts[0].(ast.Expr)
					if ok && strings.HasPrefix(render(first), "http.Method") {
						path, ok := c.Elts[1].(ast.Expr)
						handler, hok := c.Elts[2].(ast.Expr)
						if ok && hok {
							result.Routes = append(result.Routes, Route{src.path, set.Position(c.Pos()).Line, name, render(first), expression(path, src.pkg, map[string]bool{}, 0), render(handler), "route_table"})
						}
					}
				}
				return true
			})
			result.Functions = append(result.Functions, f)
		}
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(out, b, 0600); err != nil {
		panic(err)
	}
	fmt.Printf("functions=%d query_sites=%d route_declarations=%d\n", len(result.Functions), len(result.Queries), len(result.Routes))
}
