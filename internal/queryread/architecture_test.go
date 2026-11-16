package queryread_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// The upstream root adapter would bypass row cancellation (or register a
// duplicate driver). lib/vtab imports deliberately keep the upstream engine.
func TestAllDatabaseUsersUseCancellationAdapter(t *testing.T) {
	paths, err := databaseAdapterBypasses(os.DirFS(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Errorf("%s bypasses the cancellation adapter", path)
	}
}

func databaseAdapterBypasses(source fs.FS) ([]string, error) {
	var paths []string
	err := fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor") {
				return fs.SkipDir
			}
			// Nested modules do not belong to this application's package tree.
			if path != "." {
				if _, err := fs.Stat(source, path+"/go.mod"); err == nil {
					return fs.SkipDir
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, content, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if name == "modernc.org/sqlite" {
				paths = append(paths, path)
			}
		}
		return nil
	})
	return paths, err
}

func TestDatabaseAdapterCheckStaysWithinModule(t *testing.T) {
	const bypass = "package database\nimport _ \"modernc.org/sqlite\"\n"
	paths, err := databaseAdapterBypasses(fstest.MapFS{
		"go.mod":                  {Data: []byte("module example.com/app\n")},
		"internal/database/db.go": {Data: []byte(bypass)},
		"internal/engine/lib.go":  {Data: []byte("package engine\nimport _ \"modernc.org/sqlite/lib\"\n")},
		"nested/go.mod":           {Data: []byte("module example.com/independent\n")},
		"nested/database/db.go":   {Data: []byte(bypass)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "internal/database/db.go" {
		t.Fatalf("expected the application bypass only, got %v", paths)
	}
}
