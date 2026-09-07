package android

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"time"

	keystore "github.com/pavlo-v-chernykh/keystore-go/v4"
)

const generatedKeyAlias = "upload"

// GeneratedKeystore contains the portable material needed to sign Android builds.
type GeneratedKeystore struct {
	Keystore         []byte
	KeystorePassword string
	KeyAlias         string
	KeyPassword      string
}

// GenerateKeystore creates a JKS upload keystore with independent random passwords.
func GenerateKeystore(identifier string) (*GeneratedKeystore, error) {
	keystorePassword, err := randomPassword()
	if err != nil {
		return nil, err
	}
	keyPassword, err := randomPassword()
	if err != nil {
		return nil, err
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate android signing key: %w", err)
	}
	serialLimit := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("generate android certificate serial number: %w", err)
	}
	serialNumber.Add(serialNumber, big.NewInt(1))
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   identifier,
			Organization: []string{"xprem"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(30, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("generate android signing certificate: %w", err)
	}
	privateKeyPKCS8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("encode android signing key: %w", err)
	}

	keyStore := keystore.New()
	err = keyStore.SetPrivateKeyEntry(generatedKeyAlias, keystore.PrivateKeyEntry{
		CreationTime: now,
		PrivateKey:   privateKeyPKCS8,
		CertificateChain: []keystore.Certificate{{
			Type:    "X509",
			Content: certificate,
		}},
	}, []byte(keyPassword))
	if err != nil {
		return nil, fmt.Errorf("create android keystore entry: %w", err)
	}
	var encoded bytes.Buffer
	if err := keyStore.Store(&encoded, []byte(keystorePassword)); err != nil {
		return nil, fmt.Errorf("encode android keystore: %w", err)
	}
	return &GeneratedKeystore{
		Keystore:         encoded.Bytes(),
		KeystorePassword: keystorePassword,
		KeyAlias:         generatedKeyAlias,
		KeyPassword:      keyPassword,
	}, nil
}

func randomPassword() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate android keystore password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
