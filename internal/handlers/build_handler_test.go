package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"xprem/internal/android/androidtest"
	"xprem/internal/services"
	"xprem/internal/store"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

type buildCredentialRepository struct {
	services.CredentialsRepository
	credentials *store.SealedAndroidCredentials
	err         error
	id          string
}

func (repo *buildCredentialRepository) UpsertAndroidCredentials(_ context.Context, id string, credentials store.SealedAndroidCredentials) error {
	repo.credentials = &credentials
	return nil
}
func (repo *buildCredentialRepository) GetAndroidCredentials(_ context.Context, id string) (*store.SealedAndroidCredentials, error) {
	repo.id = id
	return repo.credentials, repo.err
}

type buildIdentifierRepository struct {
	services.AppIdentifierRepository
	app, id string
}

func (repo *buildIdentifierRepository) GetAppIdentifierByID(_ context.Context, app, id string) (*store.AppIdentifierRef, error) {
	repo.app, repo.id = app, id
	if app != "app-1" || id != "id-1" {
		return nil, nil
	}
	return &store.AppIdentifierRef{Id: id, Platform: services.PlatformAndroid}, nil
}
func TestBuildCredentialsAllowlistAndWholeFile(t *testing.T) {
	t.Setenv("AWSSM_DB_KEYS_MASTER_KEY_SECRET_ID", "")
	t.Setenv("DB_KEYS_MASTER_KEY_B64", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	raw := androidtest.JKSKeystore("store-password", "key-password", "selected-alias")
	repo := &buildCredentialRepository{}
	identifiers := &buildIdentifierRepository{}
	credentials := services.NewCredentialsService(repo, identifiers)
	require.NoError(t, credentials.SaveAndroidCredentials(context.Background(), "app-1", "id-1", services.AndroidCredentialsInput{
		KeystoreBase64: base64.StdEncoding.EncodeToString(raw), KeystorePassword: "store-password", KeyAlias: "selected-alias", KeyPassword: "key-password",
	}))
	unreadablePlaySecret := "not a decryptable Google Play secret"
	repo.credentials.SealedGoogleServiceAccountKey = &unreadablePlaySecret
	h := NewBuildHandler(nil, credentials)
	req := mux.SetURLVars(httptest.NewRequest("GET", "/", nil), map[string]string{"APP_ID": "app-1", "IDENTIFIER_ID": "id-1"})
	w := httptest.NewRecorder()
	h.AndroidCredentials(w, req)
	require.Equal(t, 200, w.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, map[string]string{"keystore": base64.StdEncoding.EncodeToString(raw), "keystorePassword": "store-password", "keyAlias": "selected-alias", "keyPassword": "key-password"}, got)
	require.Equal(t, "app-1", identifiers.app)
	require.Equal(t, "id-1", repo.id)
}
func TestBuildCredentialsErrorsDoNotExposeSecrets(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{errors.New("secret sentinel"), 500},
		{&store.ErrResourceNotFound{Resource: "android credentials", Identifier: "id"}, 404},
		{store.ErrNotSupportedInStatelessMode, 400},
	} {
		h := NewBuildHandler(nil, services.NewCredentialsService(&buildCredentialRepository{err: tc.err}, &buildIdentifierRepository{}))
		w := httptest.NewRecorder()
		h.AndroidCredentials(w, mux.SetURLVars(httptest.NewRequest("GET", "/", nil), map[string]string{"APP_ID": "app-1", "IDENTIFIER_ID": "id-1"}))
		require.Equal(t, tc.status, w.Code)
		require.NotContains(t, w.Body.String(), "secret sentinel")
	}
}
func TestBuildEnvironmentRejectsAmbiguousQuery(t *testing.T) {
	for _, query := range []string{"channel=a&environment=b", "channel=a&channel=b", "environment=", "channel=", "other=x", "environment=%zz", "environment=a;b"} {
		t.Run(query, func(t *testing.T) {
			h := NewBuildHandler(nil, nil) // invalid requests cannot reach the service
			w := httptest.NewRecorder()
			h.Environment(w, httptest.NewRequest("GET", "/?"+query, nil))
			require.Equal(t, 400, w.Code, w.Body.String())
		})
	}
}
