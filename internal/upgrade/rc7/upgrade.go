// Package rc7 retains the narrow prerelease conversion entrypoint.
package rc7

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/upgrade/v1"
)

const SourceCommit = v1.SourceCommit
const SourceSchemaVersion = v1.SourceSchemaVersion
const SourceManifestHash = v1.SourceManifestHash

type Options = v1.Options
type Result = v1.Result

func Run(ctx context.Context, path string, vault *secret.Vault, options Options) (Result, error) {
	return v1.RunRC7(ctx, path, vault, options)
}
