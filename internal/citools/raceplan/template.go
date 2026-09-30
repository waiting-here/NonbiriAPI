package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func prepareSharedTemplate(goTool, dir string, output io.Writer) (resultErr error) {
	source, err := currentSource(goTool)
	if err != nil {
		return err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.RemoveAll(dir))
		}
	}()
	fixture, err := prepareRunnerFixture(goTool, source.Tree)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, fixture.cleanup()) }()
	image, err := os.ReadFile(fixture.path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, fixture.identity+".sqlite"), image, 0400); err != nil {
		return err
	}
	metadata := preparedRun{Version: 1, Source: source, TemplateID: fixture.identity, TemplateSHA256: fixture.sha256, TemplateBytes: fixture.size}
	body, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "template.json"), append(body, '\n'), 0600); err != nil {
		return err
	}
	fmt.Fprintf(output, "raceplan: shared ordinary/race template built once in %s sha256=%s bytes=%d\n", fixture.buildTime, fixture.sha256, fixture.size)
	return nil
}

func readSharedTemplate(goTool, dir string) (runnerFixture, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return runnerFixture{}, err
	}
	file, err := os.Open(filepath.Join(dir, "template.json"))
	if err != nil {
		return runnerFixture{}, err
	}
	var metadata preparedRun
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&metadata)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = errors.New("template metadata trailing data")
		}
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return runnerFixture{}, err
	}
	source, err := currentSource(goTool)
	if err != nil {
		return runnerFixture{}, err
	}
	if metadata.Version != 1 || metadata.Source != source {
		return runnerFixture{}, errors.New("shared template source/toolchain mismatch")
	}
	identity, err := hex.DecodeString(metadata.TemplateID)
	if err != nil || len(identity) != sha256.Size {
		return runnerFixture{}, errors.New("shared template identity invalid")
	}
	path := filepath.Join(dir, metadata.TemplateID+".sqlite")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return runnerFixture{}, errors.New("shared template must be regular")
	}
	image, err := os.ReadFile(path)
	if err != nil {
		return runnerFixture{}, err
	}
	digest := sha256.Sum256(image)
	if len(image) != metadata.TemplateBytes || len(image) < 100 || hex.EncodeToString(digest[:]) != metadata.TemplateSHA256 {
		return runnerFixture{}, errors.New("shared template checksum/size mismatch")
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return runnerFixture{}, errors.New("shared template has sidecar")
		}
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return runnerFixture{}, err
	}
	if err := os.Chmod(path, 0400); err != nil {
		return runnerFixture{}, err
	}
	return runnerFixture{path: path, identity: metadata.TemplateID, sha256: metadata.TemplateSHA256, size: metadata.TemplateBytes}, nil
}
func exportTemplateEnvironment(goTool, dir string, output io.Writer) error {
	fixture, err := readSharedTemplate(goTool, dir)
	if err != nil {
		return err
	}
	for _, value := range fixture.childEnvironment(nil) {
		if _, err := fmt.Fprintln(output, value); err != nil {
			return err
		}
	}
	return nil
}
