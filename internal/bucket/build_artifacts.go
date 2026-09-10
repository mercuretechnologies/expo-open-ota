package bucket

import (
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

type BuildArtifact struct {
	IdentifierID string
	BuildID      string
	Type         types.BuildArtifactType
}

func (r BuildArtifact) Validate() error {
	if _, err := r.Type.Platform(); err != nil {
		return err
	}
	if err := validateUUID("identifierId", r.IdentifierID); err != nil {
		return err
	}
	return validateUUID("buildId", r.BuildID)
}

// Key is builds/{platform}/{identifierId}/{buildId}.{type}, with an
// .uploads/ segment before the file name for the staging copy.
func (r BuildArtifact) Key(staging bool) (string, error) {
	platform, err := r.Type.Platform()
	if err != nil {
		return "", err
	}
	folder := BuildsPrefix + "/" + string(platform) + "/" + r.IdentifierID + "/"
	if staging {
		folder += buildStagingDir + "/"
	}
	return folder + r.BuildID + "." + string(r.Type), nil
}
