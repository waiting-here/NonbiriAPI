// Package kit distributes the same headers and examples used by the compiler.
package kit

import (
	"archive/zip"
	"bytes"
	"embed"
	"io/fs"
	"strings"
	"sync"
)

//go:embed files
var files embed.FS

func SDK() fs.FS {
	sdk, err := fs.Sub(files, "files/sdk")
	if err != nil {
		panic(err)
	}
	return sdk
}

func Sample(game string) ([]byte, error) { return files.ReadFile("files/" + game + "/sample.json") }
func Catalog(game, mode string) ([]byte, error) {
	return files.ReadFile("files/" + game + "/" + mode + "-catalog.json")
}

var archives = func() map[string]func() ([]byte, error) {
	m := make(map[string]func() ([]byte, error), 3)
	for _, game := range []string{"bidding", "likes", "gwent"} {
		m[game] = sync.OnceValues(func() ([]byte, error) { return buildArchive(game) })
	}
	return m
}()

func Archive(game string) ([]byte, error) {
	build, ok := archives[game]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return build()
}

func buildArchive(game string) ([]byte, error) {
	var buf bytes.Buffer
	out := zip.NewWriter(&buf)
	paths := []string{"README.md", "sdk/main.cpp", "sdk/arena.hpp", "sdk/json.hpp", "sdk/LICENSE.nlohmann", game + "/bot.cpp", game + "/sample.json", game + "/standard-catalog.json"}
	if game == "likes" {
		paths = append(paths, game+"/quick-catalog.json")
	}
	for _, path := range paths {
		content, err := files.ReadFile("files/" + path)
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(path, game+"/")
		if name == "standard-catalog.json" {
			name = "catalog.json"
		}
		f, err := out.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err = f.Write(content); err != nil {
			return nil, err
		}
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
