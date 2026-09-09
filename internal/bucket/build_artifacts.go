package bucket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"xprem/internal/providers/aws"
	"xprem/internal/providers/azure"
	"xprem/internal/providers/gcp"
	"xprem/internal/types"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"google.golang.org/api/iterator"
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

type BuildArtifactStorage struct {
	bucket      Bucket
	namespaceOK atomic.Bool
}

func NewBuildArtifactStorage(b Bucket) *BuildArtifactStorage {
	return &BuildArtifactStorage{bucket: UnwrapBucket(b)}
}

func (s *BuildArtifactStorage) Get(ctx context.Context, ref BuildArtifact, staging bool) (*types.BucketFile, error) {
	key, err := ref.Key(staging)
	if err != nil {
		return nil, err
	}
	switch b := s.bucket.(type) {
	case *LocalBucket:
		return b.openFile(filepath.Join(b.rootPath(), filepath.FromSlash(key)))
	case *S3Bucket:
		return b.getObject(ctx, b.prefixedKey(key))
	case *GCSBucket:
		return b.getObject(ctx, b.prefixedKey(key))
	case *AzureBucket:
		return b.getObject(ctx, b.prefixedKey(key))
	default:
		return nil, fmt.Errorf("unsupported build storage")
	}
}

func (s *BuildArtifactStorage) Put(ctx context.Context, ref BuildArtifact, staging bool, body io.Reader) error {
	key, err := ref.Key(staging)
	if err != nil {
		return err
	}
	if err := s.CheckNamespace(ctx); err != nil {
		return err
	}
	switch b := s.bucket.(type) {
	case *LocalBucket:
		if b.BasePath == "" {
			return errors.New("BasePath not set")
		}
		return writeFileAtomically(filepath.Join(b.rootPath(), filepath.FromSlash(key)), body)
	case *S3Bucket:
		return b.putObject(ctx, b.prefixedKey(key), body)
	case *GCSBucket:
		return b.putObject(ctx, b.prefixedKey(key), body)
	case *AzureBucket:
		return b.putObject(ctx, b.prefixedKey(key), body)
	default:
		return fmt.Errorf("unsupported build storage")
	}
}

func writeFileAtomically(target string, body io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".upload-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, copyErr := io.Copy(f, body)
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), target)
}

// PresignUpload returns an empty URL on the local backend, whose uploads go
// through the server.
func (s *BuildArtifactStorage) PresignUpload(ctx context.Context, ref BuildArtifact) (string, map[string]string, error) {
	key, err := ref.Key(true)
	if err != nil {
		return "", nil, err
	}
	if err := s.CheckNamespace(ctx); err != nil {
		return "", nil, err
	}
	switch b := s.bucket.(type) {
	case *LocalBucket:
		return "", nil, nil
	case *S3Bucket:
		client, err := aws.GetS3Client()
		if err != nil {
			return "", nil, err
		}
		request, err := s3.NewPresignClient(client).PresignPutObject(ctx, &s3.PutObjectInput{Bucket: awssdk.String(b.BucketName), Key: awssdk.String(b.prefixedKey(key))}, func(options *s3.PresignOptions) { options.Expires = buildUploadExpiry })
		if err != nil {
			return "", nil, err
		}
		return request.URL, nil, nil
	case *GCSBucket:
		url, err := gcp.SignedURL(b.BucketName, b.prefixedKey(key), "PUT", "", buildUploadExpiry)
		return url, nil, err
	case *AzureBucket:
		url, err := azure.SignBlobSAS(b.ContainerName, b.prefixedKey(key), sas.BlobPermissions{Create: true, Write: true}, buildUploadExpiry)
		return url, map[string]string{"x-ms-blob-type": "BlockBlob"}, err
	default:
		return "", nil, fmt.Errorf("unsupported build storage")
	}
}

// Delete removes exactly one object and is a no-op when it is absent.
func (s *BuildArtifactStorage) Delete(ctx context.Context, ref BuildArtifact, staging bool) error {
	key, err := ref.Key(staging)
	if err != nil {
		return err
	}
	switch b := s.bucket.(type) {
	case *LocalBucket:
		if b.BasePath == "" {
			return errors.New("BasePath not set")
		}
		target := filepath.Join(b.rootPath(), filepath.FromSlash(key))
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		pruneEmptyBuildDirs(filepath.Join(b.rootPath(), BuildsPrefix), filepath.Dir(target))
		return nil
	case *S3Bucket:
		client, err := aws.GetS3Client()
		if err != nil {
			return err
		}
		_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: awssdk.String(b.BucketName), Key: awssdk.String(b.prefixedKey(key))})
		return err
	case *GCSBucket:
		bh, err := b.bucketHandle(ctx)
		if err != nil {
			return err
		}
		err = bh.Object(b.prefixedKey(key)).Delete(ctx)
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil
		}
		return err
	case *AzureBucket:
		cc, err := b.containerClient()
		if err != nil {
			return err
		}
		_, err = cc.NewBlobClient(b.prefixedKey(key)).Delete(ctx, nil)
		if bloberror.HasCode(err, bloberror.BlobNotFound) {
			return nil
		}
		return err
	default:
		return fmt.Errorf("unsupported build storage")
	}
}

// pruneEmptyBuildDirs removes dir and its empty parents, stopping at root.
func pruneEmptyBuildDirs(root, dir string) {
	for {
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// CheckNamespace rejects legacy OTA directories under builds/ before the
// first artifact write. Successful probes are cached for this storage instance.
func (s *BuildArtifactStorage) CheckNamespace(ctx context.Context) error {
	if s.namespaceOK.Load() {
		return nil
	}
	platforms, err := s.listChildPrefixes(ctx, BuildsPrefix+"/")
	if err != nil {
		return fmt.Errorf("probe build storage: %w", err)
	}
	for _, name := range platforms {
		if _, err := types.ParsePlatform(name); err != nil {
			return fmt.Errorf("%w: found %s/%s/", ErrBuildNamespaceConflict, BuildsPrefix, name)
		}
		if err := s.checkPlatformNamespace(ctx, BuildsPrefix+"/"+name+"/"); err != nil {
			return err
		}
	}
	s.namespaceOK.Store(true)
	return nil
}

func (s *BuildArtifactStorage) checkPlatformNamespace(ctx context.Context, platformFolder string) error {
	identifiers, err := s.listChildPrefixes(ctx, platformFolder)
	if err != nil {
		return fmt.Errorf("probe build storage: %w", err)
	}
	for _, name := range identifiers {
		if !canonicalUUID(name) {
			return fmt.Errorf("%w: found %s%s/", ErrBuildNamespaceConflict, platformFolder, name)
		}
		folder := platformFolder + name + "/"
		children, err := s.listChildPrefixes(ctx, folder)
		if err != nil {
			return fmt.Errorf("probe build storage: %w", err)
		}
		for _, child := range children {
			if child != buildStagingDir {
				return fmt.Errorf("%w: found %s%s/", ErrBuildNamespaceConflict, folder, child)
			}
		}
	}
	return nil
}

// listChildPrefixes returns the immediate child directories of a
// prefix-relative folder, at most buildProbeMaxPrefix of them.
func (s *BuildArtifactStorage) listChildPrefixes(ctx context.Context, folder string) ([]string, error) {
	var names []string
	switch b := s.bucket.(type) {
	case *LocalBucket:
		if b.BasePath == "" {
			return nil, errors.New("BasePath not set")
		}
		entries, err := os.ReadDir(filepath.Join(b.rootPath(), filepath.FromSlash(folder)))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
	case *S3Bucket:
		if b.BucketName == "" {
			return nil, errors.New("BucketName not set")
		}
		client, err := aws.GetS3Client()
		if err != nil {
			return nil, err
		}
		full := b.prefixedKey(folder)
		paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: awssdk.String(b.BucketName), Prefix: awssdk.String(full), Delimiter: awssdk.String("/")})
		for paginator.HasMorePages() && len(names) < buildProbeMaxPrefix {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, common := range page.CommonPrefixes {
				names = append(names, strings.TrimSuffix(strings.TrimPrefix(*common.Prefix, full), "/"))
			}
		}
	case *GCSBucket:
		bh, err := b.bucketHandle(ctx)
		if err != nil {
			return nil, err
		}
		full := b.prefixedKey(folder)
		it := bh.Objects(ctx, &storage.Query{Prefix: full, Delimiter: "/"})
		for len(names) < buildProbeMaxPrefix {
			attrs, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			if attrs.Prefix != "" {
				names = append(names, strings.TrimSuffix(strings.TrimPrefix(attrs.Prefix, full), "/"))
			}
		}
	case *AzureBucket:
		cc, err := b.containerClient()
		if err != nil {
			return nil, err
		}
		full := b.prefixedKey(folder)
		pager := cc.NewListBlobsHierarchyPager("/", &container.ListBlobsHierarchyOptions{Prefix: &full})
		for pager.More() && len(names) < buildProbeMaxPrefix {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, blobPrefix := range page.Segment.BlobPrefixes {
				if blobPrefix.Name != nil {
					names = append(names, strings.TrimSuffix(strings.TrimPrefix(*blobPrefix.Name, full), "/"))
				}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported build storage")
	}
	if len(names) >= buildProbeMaxPrefix {
		return nil, fmt.Errorf("build storage namespace probe exceeds %d directories", buildProbeMaxPrefix)
	}
	return names, nil
}
