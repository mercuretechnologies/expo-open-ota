package bucket

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func blobHash(content []byte) string {
	sum := sha256.Sum256(content)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func requireNoLeftovers(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		require.False(t, strings.HasPrefix(d.Name(), ".upload-"), "temporary file left behind: %s", path)
		return nil
	}))
}

func TestHandleUploadFileVerifiesBlobsAndWritesAtomically(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCAL_BUCKET_BASE_PATH", root)
	t.Setenv("BUCKET_KEY_PREFIX", "")
	t.Setenv("S3_KEY_PREFIX", "")
	content := []byte("bundle bytes")
	blob := filepath.Join(root, "app-1", casDir, blobHash(content))

	require.NoError(t, HandleUploadFile("app-1", blob, bytes.NewReader(content)))
	written, err := os.ReadFile(blob)
	require.NoError(t, err)
	require.Equal(t, content, written)

	tampered := filepath.Join(root, "app-1", casDir, blobHash([]byte("what the CLI hashed")))
	require.ErrorIs(t, HandleUploadFile("app-1", tampered, bytes.NewReader([]byte("what it sent"))), ErrBlobHashMismatch)
	_, err = os.Stat(tampered)
	require.True(t, os.IsNotExist(err), "a blob that does not match its hash is not written")

	interrupted := filepath.Join(root, "app-1", casDir, blobHash([]byte("full payload")))
	body := io.MultiReader(strings.NewReader("full pay"), iotest.ErrReader(errors.New("connection reset")))
	require.Error(t, HandleUploadFile("app-1", interrupted, body))
	_, err = os.Stat(interrupted)
	require.True(t, os.IsNotExist(err), "an interrupted upload leaves no partial blob")

	configFile := filepath.Join(root, "app-1", "production", "1", "1737455526", "metadata.json")
	require.NoError(t, HandleUploadFile("app-1", configFile, strings.NewReader(`{"version":0}`)))
	written, err = os.ReadFile(configFile)
	require.NoError(t, err)
	require.Equal(t, `{"version":0}`, string(written), "update folder files are stored without a hash check")

	requireNoLeftovers(t, root)
}

func TestPutBlobRejectsMismatchedHash(t *testing.T) {
	root := t.TempDir()
	b := &LocalBucket{BasePath: root}
	content := []byte("blob")
	require.NoError(t, b.PutBlob(context.Background(), "app-1", blobHash(content), bytes.NewReader(content)))
	exists, err := b.BlobExists(context.Background(), "app-1", blobHash(content))
	require.NoError(t, err)
	require.True(t, exists)

	other := blobHash([]byte("other"))
	require.ErrorIs(t, b.PutBlob(context.Background(), "app-1", other, bytes.NewReader(content)), ErrBlobHashMismatch)
	exists, err = b.BlobExists(context.Background(), "app-1", other)
	require.NoError(t, err)
	require.False(t, exists)
	requireNoLeftovers(t, root)
}
