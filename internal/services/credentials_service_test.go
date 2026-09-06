// Service-level tests for the Android credentials vault: sealing (with the
// identifier-bound AAD), validation, identifier resolution, the metadata
// projection, audit emission and the stateless-mode refusal. SQL persistence
// is covered by the store tests.
package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"xprem/internal/android"
	"xprem/internal/android/androidtest"
	"xprem/internal/auditlog"
	"xprem/internal/crypto"
	"xprem/internal/store"
	"xprem/internal/validation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeIdentifierRepo struct {
	byId map[string]store.AppIdentifierRef
	// appId every identifier belongs to; a mismatch resolves to nil.
	appId string
}

func newFakeIdentifierRepo(appId string) *fakeIdentifierRepo {
	return &fakeIdentifierRepo{byId: map[string]store.AppIdentifierRef{}, appId: appId}
}

func (f *fakeIdentifierRepo) add(id, platform, identifier string) {
	f.byId[id] = store.AppIdentifierRef{Id: id, Platform: platform, Identifier: identifier}
}

func (f *fakeIdentifierRepo) InsertAppIdentifier(_ context.Context, _ string, _ string, _ string) (string, error) {
	panic("not used in credentials tests")
}

func (f *fakeIdentifierRepo) GetAppIdentifiers(_ context.Context, _ string) ([]store.AppIdentifierRow, error) {
	panic("not used in credentials tests")
}

func (f *fakeIdentifierRepo) GetAppIdentifierByID(_ context.Context, appId string, identifierId string) (*store.AppIdentifierRef, error) {
	if appId != f.appId {
		return nil, nil
	}
	ref, ok := f.byId[identifierId]
	if !ok {
		return nil, nil
	}
	return &ref, nil
}

func (f *fakeIdentifierRepo) DeleteAppIdentifier(_ context.Context, _ string, _ string) error {
	panic("not used in credentials tests")
}

func (f *fakeIdentifierRepo) SetBuildNumber(_ context.Context, _ string, _ string, _ int64) error {
	panic("not used in credentials tests")
}

type fakeCredentialsRepo struct {
	byIdentifierId map[string]store.SealedAndroidCredentials
}

func newFakeCredentialsRepo() *fakeCredentialsRepo {
	return &fakeCredentialsRepo{byIdentifierId: map[string]store.SealedAndroidCredentials{}}
}

func (f *fakeCredentialsRepo) UpsertAndroidCredentials(_ context.Context, identifierId string, credentials store.SealedAndroidCredentials) error {
	if existing, ok := f.byIdentifierId[identifierId]; ok {
		credentials.SealedGoogleServiceAccountKey = existing.SealedGoogleServiceAccountKey
		credentials.GoogleServiceAccountEmail = existing.GoogleServiceAccountEmail
		credentials.GoogleServiceAccountProjectID = existing.GoogleServiceAccountProjectID
	}
	f.byIdentifierId[identifierId] = credentials
	return nil
}

func (f *fakeCredentialsRepo) GetAndroidCredentials(_ context.Context, identifierId string) (*store.SealedAndroidCredentials, error) {
	credentials, ok := f.byIdentifierId[identifierId]
	if !ok {
		return nil, nil
	}
	return &credentials, nil
}

func (f *fakeCredentialsRepo) UpdateGooglePlayServiceAccountKey(_ context.Context, identifierId string, sealedKey, email, projectID *string) error {
	credentials, ok := f.byIdentifierId[identifierId]
	if !ok {
		return &store.ErrResourceNotFound{Resource: "android credentials", Identifier: identifierId}
	}
	credentials.SealedGoogleServiceAccountKey = sealedKey
	credentials.GoogleServiceAccountEmail = email
	credentials.GoogleServiceAccountProjectID = projectID
	f.byIdentifierId[identifierId] = credentials
	return nil
}

func (f *fakeCredentialsRepo) DeleteAndroidCredentials(_ context.Context, identifierId string) error {
	if _, ok := f.byIdentifierId[identifierId]; !ok {
		return &store.ErrResourceNotFound{Resource: "android credentials", Identifier: identifierId}
	}
	delete(f.byIdentifierId, identifierId)
	return nil
}

const testMasterKey = "0123456789abcdef0123456789abcdef"
const testServiceAccountPrivateKey = "secret"
const validServiceAccountKey = `{"type":"service_account","project_id":"play-project","client_email":"publisher@play-project.iam.gserviceaccount.com","private_key":"` + testServiceAccountPrivateKey + `"}`

const (
	testAppId        = "app-1"
	testIdentifierId = "11111111-1111-1111-1111-111111111111"
)

func setMasterKey(t *testing.T) {
	t.Helper()
	t.Setenv("AWSSM_DB_KEYS_MASTER_KEY_SECRET_ID", "")
	t.Setenv("DB_KEYS_MASTER_KEY_B64", base64.StdEncoding.EncodeToString([]byte(testMasterKey)))
}

func newCredentialsFixture() (*CredentialsService, *fakeCredentialsRepo, *fakeIdentifierRepo) {
	identifiers := newFakeIdentifierRepo(testAppId)
	identifiers.add(testIdentifierId, PlatformAndroid, "com.example.app")
	repo := newFakeCredentialsRepo()
	return NewCredentialsService(repo, identifiers), repo, identifiers
}

func validAndroidInput() AndroidCredentialsInput {
	return AndroidCredentialsInput{
		KeyAlias:         "upload",
		KeystoreBase64:   base64.StdEncoding.EncodeToString(androidtest.JKSKeystore("keystore-pass", "key-pass", "upload")),
		KeystorePassword: "keystore-pass",
		KeyPassword:      "key-pass",
	}
}

func TestSaveAndroidCredentialsSealsKeystoreSecrets(t *testing.T) {
	setMasterKey(t)
	service, repo, _ := newCredentialsFixture()

	input := validAndroidInput()
	require.NoError(t, service.SaveAndroidCredentials(context.Background(), testAppId, testIdentifierId, input))

	sealed := repo.byIdentifierId[testIdentifierId]
	assert.Equal(t, "upload", sealed.KeyAlias)

	keystore, err := crypto.UnsealAESGCM(sealed.SealedKeystore, []byte(testMasterKey), androidCredentialAAD(testIdentifierId, "keystore"))
	require.NoError(t, err)
	sentKeystore, err := base64.StdEncoding.DecodeString(input.KeystoreBase64)
	require.NoError(t, err)
	assert.Equal(t, sentKeystore, keystore)
	keystorePassword, err := crypto.UnsealAESGCM(sealed.SealedKeystorePassword, []byte(testMasterKey), androidCredentialAAD(testIdentifierId, "keystore_password"))
	require.NoError(t, err)
	assert.Equal(t, "keystore-pass", string(keystorePassword))
	keyPassword, err := crypto.UnsealAESGCM(sealed.SealedKeyPassword, []byte(testMasterKey), androidCredentialAAD(testIdentifierId, "key_password"))
	require.NoError(t, err)
	assert.Equal(t, "key-pass", string(keyPassword))
	assert.Nil(t, sealed.SealedGoogleServiceAccountKey)

	// No sealed field is readable under another identifier's binding.
	_, err = crypto.UnsealAESGCM(sealed.SealedKeystore, []byte(testMasterKey), androidCredentialAAD("22222222-2222-2222-2222-222222222222", "keystore"))
	assert.Error(t, err)
}

func TestSaveAndroidCredentialsResolvesTheIdentifier(t *testing.T) {
	setMasterKey(t)
	service, _, identifiers := newCredentialsFixture()
	ctx := context.Background()

	// Unknown identifier id.
	err := service.SaveAndroidCredentials(ctx, testAppId, "33333333-3333-3333-3333-333333333333", validAndroidInput())
	notFoundErr := (*store.ErrResourceNotFound)(nil)
	assert.ErrorAs(t, err, &notFoundErr)

	// Identifier of another app resolves to not-found too.
	err = service.SaveAndroidCredentials(ctx, "other-app", testIdentifierId, validAndroidInput())
	assert.ErrorAs(t, err, &notFoundErr)

	// An ios identifier cannot carry android credentials.
	identifiers.add("44444444-4444-4444-4444-444444444444", PlatformIOS, "com.example.app")
	err = service.SaveAndroidCredentials(ctx, testAppId, "44444444-4444-4444-4444-444444444444", validAndroidInput())
	var valErr *validation.Error
	assert.ErrorAs(t, err, &valErr)
}

func TestSaveAndroidCredentialsRejectsInvalidInput(t *testing.T) {
	setMasterKey(t)
	service, _, _ := newCredentialsFixture()
	ctx := context.Background()

	for name, mutate := range map[string]func(*AndroidCredentialsInput){
		"empty alias":        func(i *AndroidCredentialsInput) { i.KeyAlias = "" },
		"empty keystore pwd": func(i *AndroidCredentialsInput) { i.KeystorePassword = "" },
		"empty key pwd":      func(i *AndroidCredentialsInput) { i.KeyPassword = "" },
		"bad base64":         func(i *AndroidCredentialsInput) { i.KeystoreBase64 = "not base64!!" },
		"empty keystore":     func(i *AndroidCredentialsInput) { i.KeystoreBase64 = "" },
		"garbage keystore": func(i *AndroidCredentialsInput) {
			i.KeystoreBase64 = base64.StdEncoding.EncodeToString([]byte("not a keystore"))
		},
		"wrong keystore pwd": func(i *AndroidCredentialsInput) { i.KeystorePassword = "wrong" },
		"wrong key pwd":      func(i *AndroidCredentialsInput) { i.KeyPassword = "wrong" },
		"unknown alias":      func(i *AndroidCredentialsInput) { i.KeyAlias = "release" },
	} {
		input := validAndroidInput()
		mutate(&input)
		err := service.SaveAndroidCredentials(ctx, testAppId, testIdentifierId, input)
		require.Error(t, err, name)
		var valErr *validation.Error
		assert.ErrorAs(t, err, &valErr, name)
	}
}

func TestAndroidCredentialsMetadataCarriesNoSecret(t *testing.T) {
	setMasterKey(t)
	service, _, _ := newCredentialsFixture()
	require.NoError(t, service.SaveAndroidCredentials(context.Background(), testAppId, testIdentifierId, validAndroidInput()))
	require.NoError(t, service.SaveGooglePlayServiceAccountKey(context.Background(), testAppId, testIdentifierId, validServiceAccountKey))

	metadata, err := service.GetAndroidCredentialsMetadata(context.Background(), testAppId, testIdentifierId)
	require.NoError(t, err)
	require.NotNil(t, metadata)
	assert.Equal(t, "com.example.app", metadata.Identifier)
	assert.Equal(t, "upload", metadata.KeyAlias)
	assert.True(t, metadata.HasGoogleServiceAccountKey)
	assert.Equal(t, "publisher@play-project.iam.gserviceaccount.com", metadata.GoogleServiceAccountEmail)
	assert.Equal(t, "play-project", metadata.GoogleServiceAccountProjectID)
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private_key")
	assert.NotContains(t, string(encoded), testServiceAccountPrivateKey)
}

func TestAndroidCredentialsMetadataDoesNotDecryptServiceAccountKey(t *testing.T) {
	service, repo, _ := newCredentialsFixture()
	email := "publisher@play-project.iam.gserviceaccount.com"
	projectID := "play-project"
	invalidCiphertext := "not-a-sealed-service-account"
	repo.byIdentifierId[testIdentifierId] = store.SealedAndroidCredentials{
		KeyAlias:                      "upload",
		SealedGoogleServiceAccountKey: &invalidCiphertext,
		GoogleServiceAccountEmail:     &email,
		GoogleServiceAccountProjectID: &projectID,
	}

	metadata, err := service.GetAndroidCredentialsMetadata(context.Background(), testAppId, testIdentifierId)
	require.NoError(t, err)
	require.NotNil(t, metadata)
	assert.Equal(t, email, metadata.GoogleServiceAccountEmail)
	assert.Equal(t, projectID, metadata.GoogleServiceAccountProjectID)
}

func TestGenerateAndroidCredentialsReplacesKeystoreAndPreservesServiceAccount(t *testing.T) {
	setMasterKey(t)
	service, repo, _ := newCredentialsFixture()
	input := validAndroidInput()
	require.NoError(t, service.SaveAndroidCredentials(context.Background(), testAppId, testIdentifierId, input))
	require.NoError(t, service.SaveGooglePlayServiceAccountKey(context.Background(), testAppId, testIdentifierId, validServiceAccountKey))
	previous := repo.byIdentifierId[testIdentifierId]

	require.NoError(t, service.GenerateAndroidCredentials(context.Background(), testAppId, testIdentifierId))
	generated := repo.byIdentifierId[testIdentifierId]

	assert.Equal(t, "upload", generated.KeyAlias)
	assert.NotEqual(t, previous.SealedKeystore, generated.SealedKeystore)
	assert.Equal(t, previous.SealedGoogleServiceAccountKey, generated.SealedGoogleServiceAccountKey)

	keystore, err := crypto.UnsealAESGCM(generated.SealedKeystore, []byte(testMasterKey), androidCredentialAAD(testIdentifierId, "keystore"))
	require.NoError(t, err)
	keystorePassword, err := crypto.UnsealAESGCM(generated.SealedKeystorePassword, []byte(testMasterKey), androidCredentialAAD(testIdentifierId, "keystore_password"))
	require.NoError(t, err)
	keyPassword, err := crypto.UnsealAESGCM(generated.SealedKeyPassword, []byte(testMasterKey), androidCredentialAAD(testIdentifierId, "key_password"))
	require.NoError(t, err)
	require.NoError(t, android.ValidateKeystore(keystore, string(keystorePassword), string(keyPassword), generated.KeyAlias))
}

func TestKeystoreAndGooglePlayServiceAccountChangeIndependently(t *testing.T) {
	setMasterKey(t)
	service, repo, _ := newCredentialsFixture()
	require.NoError(t, service.SaveAndroidCredentials(context.Background(), testAppId, testIdentifierId, validAndroidInput()))
	originalKeystore := repo.byIdentifierId[testIdentifierId]

	require.NoError(t, service.SaveGooglePlayServiceAccountKey(context.Background(), testAppId, testIdentifierId, validServiceAccountKey))
	withServiceAccount := repo.byIdentifierId[testIdentifierId]
	assert.Equal(t, originalKeystore.SealedKeystore, withServiceAccount.SealedKeystore)
	assert.Equal(t, originalKeystore.SealedKeystorePassword, withServiceAccount.SealedKeystorePassword)
	assert.Equal(t, originalKeystore.SealedKeyPassword, withServiceAccount.SealedKeyPassword)
	require.NotNil(t, withServiceAccount.SealedGoogleServiceAccountKey)

	replacement := validAndroidInput()
	replacement.KeystorePassword = "replacement-store-password"
	replacement.KeyPassword = "replacement-key-password"
	replacement.KeystoreBase64 = base64.StdEncoding.EncodeToString(androidtest.JKSKeystore(replacement.KeystorePassword, replacement.KeyPassword, replacement.KeyAlias))
	require.NoError(t, service.SaveAndroidCredentials(context.Background(), testAppId, testIdentifierId, replacement))
	replacedKeystore := repo.byIdentifierId[testIdentifierId]
	assert.NotEqual(t, originalKeystore.SealedKeystore, replacedKeystore.SealedKeystore)
	assert.Equal(t, withServiceAccount.SealedGoogleServiceAccountKey, replacedKeystore.SealedGoogleServiceAccountKey)

	require.NoError(t, service.DeleteGooglePlayServiceAccountKey(context.Background(), testAppId, testIdentifierId))
	withoutServiceAccount := repo.byIdentifierId[testIdentifierId]
	assert.Equal(t, replacedKeystore.SealedKeystore, withoutServiceAccount.SealedKeystore)
	assert.Nil(t, withoutServiceAccount.SealedGoogleServiceAccountKey)
}

func TestSaveGooglePlayServiceAccountKeyRejectsInvalidJSON(t *testing.T) {
	setMasterKey(t)
	service, _, _ := newCredentialsFixture()
	require.NoError(t, service.SaveAndroidCredentials(context.Background(), testAppId, testIdentifierId, validAndroidInput()))

	err := service.SaveGooglePlayServiceAccountKey(context.Background(), testAppId, testIdentifierId, "{broken")
	var valErr *validation.Error
	assert.ErrorAs(t, err, &valErr)
	err = service.SaveGooglePlayServiceAccountKey(context.Background(), testAppId, testIdentifierId, `{}`)
	assert.ErrorAs(t, err, &valErr)
}

func TestExportAndroidKeystoreUnsealsPortableCredentials(t *testing.T) {
	setMasterKey(t)
	service, _, _ := newCredentialsFixture()
	input := validAndroidInput()
	require.NoError(t, service.SaveAndroidCredentials(context.Background(), testAppId, testIdentifierId, input))

	exported, err := service.ExportAndroidKeystore(context.Background(), testAppId, testIdentifierId)
	require.NoError(t, err)

	assert.Equal(t, input.KeyAlias, exported.KeyAlias)
	assert.Equal(t, input.KeystorePassword, exported.KeystorePassword)
	assert.Equal(t, input.KeyPassword, exported.KeyPassword)
	assert.Contains(t, string(exported.CertificatePEM), "BEGIN CERTIFICATE")
	require.NoError(t, android.ValidateKeystore(exported.Keystore, exported.KeystorePassword, exported.KeyPassword, exported.KeyAlias))
}

func TestAndroidCredentialsAuditEvents(t *testing.T) {
	setMasterKey(t)
	service, _, _ := newCredentialsFixture()
	var recorded []auditlog.Event
	service.SetOnAuditEvent(func(_ context.Context, event auditlog.Event) {
		recorded = append(recorded, event)
	})

	require.NoError(t, service.SaveAndroidCredentials(context.Background(), testAppId, testIdentifierId, validAndroidInput()))
	require.NoError(t, service.DeleteAndroidCredentials(context.Background(), testAppId, testIdentifierId))

	require.Len(t, recorded, 2)
	assert.Equal(t, auditlog.ActionAndroidCredentialsSaved, recorded[0].Action)
	assert.Equal(t, testIdentifierId, recorded[0].TargetID)
	assert.Equal(t, "com.example.app", recorded[0].TargetDisplay)
	assert.NotContains(t, recorded[0].Metadata, "keystore")
	assert.Equal(t, auditlog.ActionAndroidCredentialsDeleted, recorded[1].Action)
	assert.Equal(t, "com.example.app", recorded[1].TargetDisplay)
}

func TestAndroidCredentialsUnsupportedInStatelessMode(t *testing.T) {
	service := NewCredentialsService(nil, nil)
	ctx := context.Background()
	assert.ErrorIs(t, service.SaveAndroidCredentials(ctx, testAppId, testIdentifierId, validAndroidInput()), store.ErrNotSupportedInStatelessMode)
	_, err := service.GetAndroidCredentialsMetadata(ctx, testAppId, testIdentifierId)
	assert.ErrorIs(t, err, store.ErrNotSupportedInStatelessMode)
	assert.ErrorIs(t, service.DeleteAndroidCredentials(ctx, testAppId, testIdentifierId), store.ErrNotSupportedInStatelessMode)
	assert.ErrorIs(t, service.GenerateAndroidCredentials(ctx, testAppId, testIdentifierId), store.ErrNotSupportedInStatelessMode)
	_, err = service.ExportAndroidKeystore(ctx, testAppId, testIdentifierId)
	assert.ErrorIs(t, err, store.ErrNotSupportedInStatelessMode)
	assert.ErrorIs(t, service.SaveGooglePlayServiceAccountKey(ctx, testAppId, testIdentifierId, `{}`), store.ErrNotSupportedInStatelessMode)
	assert.ErrorIs(t, service.DeleteGooglePlayServiceAccountKey(ctx, testAppId, testIdentifierId), store.ErrNotSupportedInStatelessMode)
}
