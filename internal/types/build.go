package types

import (
	"fmt"
	"time"
)

// BuildStatus is where a build stands in its upload lifecycle.
type BuildStatus string

const (
	BuildStatusBuilding  BuildStatus = "building"
	BuildStatusUploading BuildStatus = "uploading"
	BuildStatusReady     BuildStatus = "ready"
	BuildStatusFailed    BuildStatus = "failed"
)

// BuildArtifactType is the file format a build produces.
type BuildArtifactType string

const (
	BuildArtifactAPK BuildArtifactType = "apk"
	BuildArtifactAAB BuildArtifactType = "aab"
	BuildArtifactIPA BuildArtifactType = "ipa"
)

// Platform is the platform that produces this artifact type.
func (t BuildArtifactType) Platform() (Platform, error) {
	switch t {
	case BuildArtifactAPK, BuildArtifactAAB:
		return PlatformAndroid, nil
	case BuildArtifactIPA:
		return PlatformIOS, nil
	}
	return "", fmt.Errorf("unsupported build artifact type: %q", t)
}

// BuildMetadata carries what the CLI knows about a build; the artifact and
// timing fields stay empty until the corresponding lifecycle step reports them.
type BuildMetadata struct {
	Profile        string    `json:"profile"`
	Mode           string    `json:"mode,omitempty"`
	Environment    string    `json:"environment,omitempty"`
	Channel        string    `json:"channel,omitempty"`
	Version        string    `json:"version,omitempty"`
	BuildNumber    string    `json:"buildNumber,omitempty"`
	RuntimeVersion string    `json:"runtimeVersion,omitempty"`
	Fingerprint    string    `json:"fingerprint,omitempty"`
	ExpoSDK        string    `json:"expoSdk,omitempty"`
	CLIVersion     string    `json:"cliVersion"`
	GitCommit      string    `json:"gitCommit,omitempty"`
	GitMessage     string    `json:"gitMessage,omitempty"`
	GitDirty       bool      `json:"gitDirty,omitempty"`
	StartedAt      time.Time `json:"startedAt,omitzero"`
	FinishedAt     time.Time `json:"finishedAt,omitzero"`
	DurationMs     int64     `json:"durationMs,omitempty"`
}

type BuildRecord struct {
	ID              string            `json:"id"`
	AppID           string            `json:"appId"`
	AppIdentifierID string            `json:"appIdentifierId"`
	Platform        Platform          `json:"platform"`
	ApplicationID   string            `json:"applicationId"`
	Status          BuildStatus       `json:"status"`
	ArtifactType    BuildArtifactType `json:"artifactType"`
	Size            int64             `json:"size,omitempty"`
	SHA256          string            `json:"sha256,omitempty"`
	ArtifactKey     string            `json:"-"`
	Metadata        BuildMetadata     `json:"metadata"`
	ActorType       string            `json:"actorType"`
	ActorID         string            `json:"actorId"`
	ActorDisplay    string            `json:"actorDisplay"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	ReadyAt         *time.Time        `json:"readyAt,omitempty"`
}

type BuildShare struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

const MaxBuildLogBytes = 10 << 20
const MaxBuildLogChunkBytes = 32 << 10

type BuildLogChunk struct {
	Offset    int32     `json:"offset"`
	Content   string    `json:"content"`
	Format    string    `json:"format"`
	CreatedAt time.Time `json:"createdAt"`
}
