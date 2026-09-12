package store_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"xprem/internal/bucket"
	"xprem/internal/store"
	"xprem/internal/types"

	"github.com/stretchr/testify/require"
)

type metadataReaderBucket struct {
	bucket.Bucket
	reader io.Reader
}

func (b metadataReaderBucket) GetFile(types.Update, string) (*types.BucketFile, error) {
	return &types.BucketFile{Reader: io.NopCloser(b.reader)}, nil
}

func TestGetUpdateAssetMappingPreservesReadErrors(t *testing.T) {
	for _, metadata := range []string{`{"assetMapping":`, `{"assetMapping":{}}`} {
		t.Run(metadata, func(t *testing.T) {
			reader := io.MultiReader(strings.NewReader(metadata), iotest.ErrReader(io.ErrUnexpectedEOF))
			updateStore := store.NewBucketUpdateStore(metadataReaderBucket{reader: reader})
			mapping, err := updateStore.GetUpdateAssetMapping(context.Background(), types.Update{})
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.Nil(t, mapping)
		})
	}
}
