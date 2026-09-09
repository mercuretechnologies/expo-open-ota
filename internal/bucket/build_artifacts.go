package bucket

import (
	"context"
	"errors"
	"fmt"
	"time"
	"xprem/internal/types"
)

// BuildsPrefix is the bucket-root directory of every build artifact, a sibling
// of the {appId}/ OTA trees.
const BuildsPrefix = "builds"

const (
	buildStagingDir   = ".uploads"
	buildUploadExpiry = 10 * time.Minute
)

// ErrBuildNamespaceConflict means {prefix}builds/ already holds data that is
// not build artifacts, so build writes are refused rather than mixed into it.
var ErrBuildNamespaceConflict = errors.New("the builds/ storage prefix holds legacy OTA data; move that app's data out of builds/ before uploading build artifacts")

type BuildArtifact struct {
	IdentifierID string
	BuildID      string
	Type         types.BuildArtifactType
}

// Key is builds/{platform}/{identifierId}/{buildId}.{type}, with an
// .uploads/ segment before the file name for the staging copy.
func (r BuildArtifact) Key(staging bool) (string, error) {
	platform, err := r.Type.Platform()
	if err != nil {
		return "", err
	}
	if err := validateUUID("identifierId", r.IdentifierID); err != nil {
		return "", err
	}
	if err := validateUUID("buildId", r.BuildID); err != nil {
		return "", err
	}
	folder := BuildsPrefix + "/" + string(platform) + "/" + r.IdentifierID + "/"
	if staging {
		folder += buildStagingDir + "/"
	}
	return folder + r.BuildID + "." + string(r.Type), nil
}

// checkBuildNamespace rejects a pre-v2 branch named builds/, whose children
// are runtime versions rather than platforms.
func checkBuildNamespace(ctx context.Context, b Bucket) error {
	children, err := b.ListBuildPrefixes(ctx, BuildsPrefix+"/")
	if err != nil {
		return fmt.Errorf("probe build storage: %w", err)
	}
	for _, name := range children {
		if _, err := types.ParsePlatform(name); err != nil {
			return fmt.Errorf("%w: found %s/%s/", ErrBuildNamespaceConflict, BuildsPrefix, name)
		}
	}
	return nil
}
