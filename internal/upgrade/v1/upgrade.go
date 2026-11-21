// Package v1 upgrades stopped databases that match a supported released schema.
package v1

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const SourceCommit = "03456c51d7537e806fc1b4576fc7e9660dc3f696"
const SourceSchemaVersion = 54
const schema55Commit = "b1700f25d7a73535af76cdd15c356d92eff0d8fd"
const toolName = "nonbiri-upgrade-v1"

type Options struct {
	Apply    bool
	Progress func(Progress)
}

type Result struct {
	Tool                          string `json:"tool"`
	TargetManifestHash            string `json:"target_manifest_hash"`
	SupportedSourceSchemaVersions []int  `json:"supported_source_schema_versions"`
	Status                        string `json:"status"`
	SourceSchemaVersion           int    `json:"source_schema_version"`
	TargetSchemaVersion           int    `json:"target_schema_version"`
}

var supportedSources = []db.OfflineConversionDescriptor{
	{Tool: toolName, SourceCommit: SourceCommit, SourceSchemaVersion: SourceSchemaVersion, SourceManifestHash: db.Schema54ManifestHash, TargetSchemaVersion: db.CurrentSchemaVersion},
	{Tool: toolName, SourceCommit: schema55Commit, SourceSchemaVersion: 55, SourceManifestHash: db.Schema55ManifestHash, TargetSchemaVersion: db.CurrentSchemaVersion},
}

func SchemaDescriptor() db.StorageSchemaDescriptor {
	descriptor := db.SchemaDescriptor()
	descriptor.OfflineConversions = slices.Clone(supportedSources)
	return descriptor
}

func Run(ctx context.Context, path string, vault *secret.Vault, options Options) (Result, error) {
	if ctx == nil || vault == nil {
		return Result{}, errors.New("offline conversion context and encryption key are required")
	}
	ctx, stop := withProgress(ctx, db.CurrentSchemaVersion, options.Progress, 5*time.Second)
	defer stop()
	version, err := convertOffline(ctx, path, vault, options.Apply)
	if err != nil {
		return Result{}, err
	}
	status := "current"
	if version != db.CurrentSchemaVersion {
		status = "ready"
		if options.Apply {
			status = "converted"
		}
	}
	versions := make([]int, len(supportedSources))
	for i, source := range supportedSources {
		versions[i] = source.SourceSchemaVersion
	}
	return Result{Tool: toolName, TargetManifestHash: db.PinnedGenerationTwoManifestHash,
		SupportedSourceSchemaVersions: versions, Status: status,
		SourceSchemaVersion: version, TargetSchemaVersion: db.CurrentSchemaVersion}, nil
}
