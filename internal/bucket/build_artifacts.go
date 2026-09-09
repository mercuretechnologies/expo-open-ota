package bucket

import (
	"context"
	"errors"
	"fmt"
	"time"
	"xprem/internal/types"

	"github.com/google/uuid"
)

// BuildsPrefix is the bucket-root directory of every build artifact, a sibling
// of the {appId}/ OTA trees.
const BuildsPrefix = "builds"

const (
	buildStagingDir     = ".uploads"
	buildUploadExpiry   = 10 * time.Minute
	buildProbeMaxPrefix = 10000
)

// ErrBuildNamespaceConflict means {prefix}builds/ already holds data that is
// not build artifacts, so build writes are refused rather than mixed into it.
var ErrBuildNamespaceConflict = errors.New("the builds/ storage prefix holds legacy OTA data; move that app's data out of builds/ before uploading build artifacts")

type BuildArtifact struct {
	IdentifierID string
	BuildID      string
	Type         types.BuildArtifactType
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

// Key is builds/{platform}/{identifierId}/{buildId}.{type}, with an
// .uploads/ segment before the file name for the staging copy.
func (r BuildArtifact) Key(staging bool) (string, error) {
	platform, err := r.Type.Platform()
	if err != nil {
		return "", err
	}
	if !canonicalUUID(r.IdentifierID) || !canonicalUUID(r.BuildID) {
		return "", fmt.Errorf("invalid build artifact identifier")
	}
	folder := BuildsPrefix + "/" + string(platform) + "/" + r.IdentifierID + "/"
	if staging {
		folder += buildStagingDir + "/"
	}
	return folder + r.BuildID + "." + string(r.Type), nil
}

// checkBuildNamespace rejects legacy OTA directories under builds/.
func checkBuildNamespace(ctx context.Context, b Bucket) error {
	platforms, err := listBuildPrefixes(ctx, b, BuildsPrefix+"/")
	if err != nil {
		return err
	}
	for _, name := range platforms {
		if _, err := types.ParsePlatform(name); err != nil {
			return fmt.Errorf("%w: found %s/%s/", ErrBuildNamespaceConflict, BuildsPrefix, name)
		}
		if err := checkPlatformNamespace(ctx, b, BuildsPrefix+"/"+name+"/"); err != nil {
			return err
		}
	}
	return nil
}

func checkPlatformNamespace(ctx context.Context, b Bucket, platformFolder string) error {
	identifiers, err := listBuildPrefixes(ctx, b, platformFolder)
	if err != nil {
		return err
	}
	for _, name := range identifiers {
		if !canonicalUUID(name) {
			return fmt.Errorf("%w: found %s%s/", ErrBuildNamespaceConflict, platformFolder, name)
		}
		folder := platformFolder + name + "/"
		children, err := listBuildPrefixes(ctx, b, folder)
		if err != nil {
			return err
		}
		for _, child := range children {
			if child != buildStagingDir {
				return fmt.Errorf("%w: found %s%s/", ErrBuildNamespaceConflict, folder, child)
			}
		}
	}
	return nil
}

func listBuildPrefixes(ctx context.Context, b Bucket, folder string) ([]string, error) {
	names, err := b.ListBuildPrefixes(ctx, folder)
	if err != nil {
		return nil, fmt.Errorf("probe build storage: %w", err)
	}
	if len(names) >= buildProbeMaxPrefix {
		return nil, fmt.Errorf("build storage namespace probe exceeds %d directories", buildProbeMaxPrefix)
	}
	return names, nil
}
