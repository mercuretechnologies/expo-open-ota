package bucket

import (
	"context"
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

type BuildUpload struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
}

// RequestBuildArtifactUpload returns the upload instructions for an artifact.
// An empty URL leaves local upload authorization to the service.
func RequestBuildArtifactUpload(ctx context.Context, storage Bucket, ref BuildArtifact) (*BuildUpload, error) {
	url, err := storage.RequestBuildArtifactUploadURL(ctx, ref)
	if err != nil {
		return nil, err
	}
	upload := &BuildUpload{URL: url, Method: "PUT"}
	if provider, ok := UnwrapBucket(storage).(interface{ uploadHeaders() map[string]string }); ok {
		upload.Headers = provider.uploadHeaders()
	}
	return upload, nil
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
