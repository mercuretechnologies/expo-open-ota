package android

import (
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKeystoreProducesUsableUniqueJKS(t *testing.T) {
	first, err := GenerateKeystore("com.example.app")
	require.NoError(t, err)
	second, err := GenerateKeystore("com.example.app")
	require.NoError(t, err)

	assert.Equal(t, "upload", first.KeyAlias)
	assert.NotEmpty(t, first.KeystorePassword)
	assert.NotEmpty(t, first.KeyPassword)
	assert.NotEqual(t, first.Keystore, second.Keystore)
	assert.NotEqual(t, first.KeystorePassword, second.KeystorePassword)
	assert.NotEqual(t, first.KeyPassword, second.KeyPassword)
	require.NoError(t, ValidateKeystore(
		first.Keystore,
		first.KeystorePassword,
		first.KeyPassword,
		first.KeyAlias,
	))
	certificatePEM, err := SigningCertificatePEM(
		first.Keystore,
		first.KeystorePassword,
		first.KeyPassword,
		first.KeyAlias,
	)
	require.NoError(t, err)
	certificate, rest := pem.Decode(certificatePEM)
	require.NotNil(t, certificate)
	assert.Equal(t, "CERTIFICATE", certificate.Type)
	assert.Empty(t, rest)
}
