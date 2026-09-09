package bucket

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"xprem/internal/types"

	"github.com/stretchr/testify/require"
)

const (
	testIdentifierID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testBuildID      = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func testArtifact() BuildArtifact {
	return BuildArtifact{Platform: types.PlatformAndroid, IdentifierID: testIdentifierID, BuildID: testBuildID, Format: "apk"}
}

func TestBuildObjectKeysAreIsolatedAndValidated(t *testing.T) {
	ref := testArtifact()
	key, err := ref.Key(false)
	require.NoError(t, err)
	require.Equal(t, "builds/android/"+testIdentifierID+"/"+testBuildID+".apk", key)
	staging, err := ref.Key(true)
	require.NoError(t, err)
	require.Equal(t, "builds/android/"+testIdentifierID+"/.uploads/"+testBuildID+".apk", staging)

	for name, bad := range map[string]BuildArtifact{
		"traversal identifier": {Platform: types.PlatformAndroid, IdentifierID: "../escape", BuildID: testBuildID, Format: "apk"},
		"uppercase uuid":       {Platform: types.PlatformAndroid, IdentifierID: strings.ToUpper(testIdentifierID), BuildID: testBuildID, Format: "apk"},
		"ios":                  {Platform: types.PlatformIOS, IdentifierID: testIdentifierID, BuildID: testBuildID, Format: "apk"},
		"format":               {Platform: types.PlatformAndroid, IdentifierID: testIdentifierID, BuildID: testBuildID, Format: "apk/../x"},
		"empty build":          {Platform: types.PlatformAndroid, IdentifierID: testIdentifierID, Format: "aab"},
	} {
		_, err := bad.Key(false)
		require.Error(t, err, name)
	}
}

func TestLocalBuildStagingCannotOverwritePublishedArtifact(t *testing.T) {
	storage := NewBuildArtifactStorage(&LocalBucket{BasePath: t.TempDir(), KeyPrefix: "tenant/"})
	ref := testArtifact()
	ctx := context.Background()
	require.NoError(t, storage.Put(ctx, ref, false, strings.NewReader("verified")))
	require.NoError(t, storage.Put(ctx, ref, true, strings.NewReader("late upload")))
	file, err := storage.Get(ctx, ref, false)
	require.NoError(t, err)
	defer file.Reader.Close()
	contents, err := io.ReadAll(file.Reader)
	require.NoError(t, err)
	require.Equal(t, "verified", string(contents))
}

func TestLocalBuildStorageHonoursKeyPrefixAndLeavesNoTempFiles(t *testing.T) {
	base := t.TempDir()
	storage := NewBuildArtifactStorage(&validatingBucket{Inner: &LocalBucket{BasePath: base, KeyPrefix: "tenant/"}})
	ref := testArtifact()
	require.NoError(t, storage.Put(context.Background(), ref, true, strings.NewReader("bytes")))
	dir := filepath.Join(base, "tenant", "builds", "android", testIdentifierID, ".uploads")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, testBuildID+".apk", entries[0].Name())
}

func TestLocalBuildGetReturnsNilWhenAbsent(t *testing.T) {
	storage := NewBuildArtifactStorage(&LocalBucket{BasePath: t.TempDir()})
	file, err := storage.Get(context.Background(), testArtifact(), false)
	require.NoError(t, err)
	require.Nil(t, file)
}

func TestLocalBuildDeleteIsIdempotentAndPrunesEmptyDirectories(t *testing.T) {
	base := t.TempDir()
	storage := NewBuildArtifactStorage(&LocalBucket{BasePath: base})
	ref := testArtifact()
	ctx := context.Background()
	require.NoError(t, storage.Put(ctx, ref, true, strings.NewReader("staged")))
	require.NoError(t, storage.Put(ctx, ref, false, strings.NewReader("final")))

	require.NoError(t, storage.Delete(ctx, ref, true))
	require.NoError(t, storage.Delete(ctx, ref, true))
	_, err := os.Stat(filepath.Join(base, "builds", "android", testIdentifierID, ".uploads"))
	require.True(t, os.IsNotExist(err), "empty .uploads directory should be pruned")
	file, err := storage.Get(ctx, ref, false)
	require.NoError(t, err)
	require.NotNil(t, file, "final artifact survives a staging delete")
	file.Reader.Close()

	require.NoError(t, storage.Delete(ctx, ref, false))
	_, err = os.Stat(filepath.Join(base, "builds", "android", testIdentifierID))
	require.True(t, os.IsNotExist(err), "empty identifier directory should be pruned")
	_, err = os.Stat(filepath.Join(base, "builds"))
	require.NoError(t, err, "the builds root is never pruned")
}

func TestLocalBuildDeleteKeepsSiblings(t *testing.T) {
	base := t.TempDir()
	storage := NewBuildArtifactStorage(&LocalBucket{BasePath: base})
	ref := testArtifact()
	other := ref
	other.BuildID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	ctx := context.Background()
	require.NoError(t, storage.Put(ctx, ref, false, strings.NewReader("one")))
	require.NoError(t, storage.Put(ctx, other, false, strings.NewReader("two")))
	require.NoError(t, storage.Delete(ctx, ref, false))
	file, err := storage.Get(ctx, other, false)
	require.NoError(t, err)
	require.NotNil(t, file)
	file.Reader.Close()
}

func writeLegacyUpdate(t *testing.T, root string, segments ...string) {
	t.Helper()
	dir := filepath.Join(append([]string{root}, segments...)...)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".check"), []byte("ok"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "update-metadata.json"), []byte("{}"), 0o644))
}

func TestBuildWritesRefuseLegacyOTATreeUnderBuildsPrefix(t *testing.T) {
	for name, segments := range map[string][]string{
		"app named builds with a branch":   {"builds", "main", "1.0.0", "1674170951"},
		"app named builds, android branch": {"builds", "android", "1.0.0", "1674170951"},
		"cas of an app named builds":       {"builds", "cas"},
		"UUID runtime version":             {"builds", "android", testIdentifierID, "1674170951"},
	} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeLegacyUpdate(t, base, segments...)
			storage := NewBuildArtifactStorage(&LocalBucket{BasePath: base})
			ctx := context.Background()
			err := storage.Put(ctx, testArtifact(), true, strings.NewReader("bytes"))
			require.ErrorIs(t, err, ErrBuildNamespaceConflict)
			_, _, err = storage.PresignUpload(ctx, testArtifact())
			require.ErrorIs(t, err, ErrBuildNamespaceConflict)
			_, err = os.Stat(filepath.Join(base, "builds", "android", testIdentifierID))
			if len(segments) < 3 || segments[2] != testIdentifierID {
				require.True(t, os.IsNotExist(err), "nothing is written into the conflicting tree")
			}
			_, err = os.Stat(filepath.Join(base, "builds", "android", testIdentifierID, ".uploads"))
			require.True(t, os.IsNotExist(err))

			file, err := storage.Get(ctx, testArtifact(), false)
			require.NoError(t, err)
			require.Nil(t, file)
			require.NoError(t, storage.Delete(ctx, testArtifact(), false), "reads and exact deletes still work")
			_, err = os.Stat(filepath.Join(append([]string{base}, segments...)...))
			require.NoError(t, err, "the legacy tree is preserved")
		})
	}
}

func TestBuildNamespaceProbeIsRememberedOncePassed(t *testing.T) {
	base := t.TempDir()
	storage := NewBuildArtifactStorage(&LocalBucket{BasePath: base})
	ctx := context.Background()
	require.NoError(t, storage.CheckNamespace(ctx))
	writeLegacyUpdate(t, base, "builds", "main", "1.0.0", "1674170951")
	require.NoError(t, storage.CheckNamespace(ctx))
	require.ErrorIs(t, NewBuildArtifactStorage(&LocalBucket{BasePath: base}).CheckNamespace(ctx), ErrBuildNamespaceConflict)
}

func TestBuildTreeCoexistsWithOTATreesAndMigration(t *testing.T) {
	base := t.TempDir()
	appId := "d8471dfc-c3e9-4e14-afd9-21dc34cc498a"
	writeLegacyUpdate(t, base, appId, "main", "1.0.0", "1674170951")
	writeLegacyUpdate(t, base, "staging", "1.0.0", "1674170952")
	local := &LocalBucket{BasePath: base}
	storage := NewBuildArtifactStorage(local)
	ref := testArtifact()
	ctx := context.Background()
	require.NoError(t, storage.Put(ctx, ref, true, strings.NewReader("staged")))
	require.NoError(t, storage.Put(ctx, ref, false, strings.NewReader("final")))

	branches, err := (&validatingBucket{Inner: local}).GetBranches(appId)
	require.NoError(t, err)
	require.Equal(t, []string{"main"}, branches)
	require.False(t, local.looksLikeV1Branch(BuildsPrefix))

	require.NoError(t, local.MoveRootEntriesUnder(appId))
	_, err = os.Stat(filepath.Join(base, appId, "staging", "1.0.0", "1674170952", ".check"))
	require.NoError(t, err, "the v1 branch is re-pathed")
	_, err = os.Stat(filepath.Join(base, appId, BuildsPrefix))
	require.True(t, os.IsNotExist(err), "the builds tree is not mistaken for a v1 branch")
	file, err := storage.Get(ctx, ref, false)
	require.NoError(t, err)
	require.NotNil(t, file)
	file.Reader.Close()
	require.NoError(t, storage.CheckNamespace(ctx))
	require.NoError(t, (&validatingBucket{Inner: local}).DeleteUpdateFolder(appId, "main", "1.0.0", "1674170951"))
	_, err = os.Stat(filepath.Join(base, appId, "main", "1.0.0", "1674170951"))
	require.True(t, os.IsNotExist(err))
	file, err = storage.Get(ctx, ref, false)
	require.NoError(t, err)
	require.NotNil(t, file, "deleting an OTA update preserves the build artifact")
	file.Reader.Close()
}

func TestBuildKeysNeverConfirmAV1Triple(t *testing.T) {
	ref := testArtifact()
	for _, staging := range []bool{false, true} {
		key, err := ref.Key(staging)
		require.NoError(t, err)
		_, isMarker := v1BranchTripleFromMarker(key)
		require.False(t, isMarker)
		require.False(t, inConfirmedTriple(key, map[string]bool{}))
	}
}

func TestBuildStorageRejectsUnknownBackend(t *testing.T) {
	storage := &BuildArtifactStorage{bucket: unknownBucket{}}
	_, err := storage.Get(context.Background(), testArtifact(), false)
	require.Error(t, err)
	err = storage.Put(context.Background(), testArtifact(), false, strings.NewReader(""))
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrBuildNamespaceConflict))
}

type unknownBucket struct{ Bucket }
