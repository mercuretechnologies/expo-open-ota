package android

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"xprem/internal/validation"

	keystore "github.com/pavlo-v-chernykh/keystore-go/v4"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

const (
	jksMagic   = 0xFEEDFEED
	jceksMagic = 0xCECECECE
)

// ValidateKeystore proves the uploaded blob is a signing keystore that the
// given passwords and alias actually open.
func ValidateKeystore(data []byte, keystorePassword, keyPassword, keyAlias string) error {
	if len(data) >= 4 {
		switch binary.BigEndian.Uint32(data[:4]) {
		case jksMagic:
			return validateJKS(data, keystorePassword, keyPassword, keyAlias)
		case jceksMagic:
			return validation.Errorf("keystore", "JCEKS keystores are not supported; convert it with `keytool -importkeystore -deststoretype pkcs12`")
		}
	}
	// PKCS12 files are a DER SEQUENCE, which always starts with 0x30.
	if len(data) > 0 && data[0] == 0x30 {
		return validatePKCS12(data, keystorePassword, keyPassword, keyAlias)
	}
	return validation.Errorf("keystore", "file is not a JKS or PKCS12 keystore")
}

// SigningCertificatePEM returns the public certificate Google Play needs when
// an upload key is registered or reset. The private key never leaves the JKS.
func SigningCertificatePEM(data []byte, keystorePassword, keyPassword, keyAlias string) ([]byte, error) {
	if err := ValidateKeystore(data, keystorePassword, keyPassword, keyAlias); err != nil {
		return nil, err
	}
	var certificateDER []byte
	if binary.BigEndian.Uint32(data[:4]) == jksMagic {
		ks := keystore.New()
		if err := ks.Load(bytes.NewReader(data), []byte(keystorePassword)); err != nil {
			return nil, fmt.Errorf("open validated JKS: %w", err)
		}
		entry, err := ks.GetPrivateKeyEntry(keyAlias, []byte(keyPassword))
		if err != nil {
			return nil, fmt.Errorf("read validated JKS key: %w", err)
		}
		certificateDER = entry.CertificateChain[0].Content
	} else {
		blocks, err := pkcs12.ToPEM(data, keystorePassword)
		if err != nil {
			return nil, fmt.Errorf("read validated PKCS12 certificate: %w", err)
		}
		certificateDER, err = signingCertificateDER(blocks, keyAlias)
		if err != nil {
			return nil, err
		}
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), nil
}

func signingCertificateDER(blocks []*pem.Block, keyAlias string) ([]byte, error) {
	var publicKey []byte
	for _, block := range blocks {
		if block.Type != "PRIVATE KEY" || !strings.EqualFold(block.Headers["friendlyName"], keyAlias) {
			continue
		}
		signer, err := parsePrivateKeySigner(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse private key for alias %q: %w", keyAlias, err)
		}
		publicKey, err = x509.MarshalPKIXPublicKey(signer.Public())
		if err != nil {
			return nil, fmt.Errorf("read public key for alias %q: %w", keyAlias, err)
		}
		break
	}
	for _, block := range blocks {
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate in PKCS12 keystore: %w", err)
		}
		if bytes.Equal(publicKey, certificate.RawSubjectPublicKeyInfo) {
			return block.Bytes, nil
		}
	}
	return nil, fmt.Errorf("certificate for alias %q not found in validated PKCS12 keystore", keyAlias)
}

func parsePrivateKeySigner(der []byte) (crypto.Signer, error) {
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if signer, ok := key.(crypto.Signer); ok {
			return signer, nil
		}
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	return nil, errors.New("unsupported private key encoding")
}

func validateJKS(data []byte, keystorePassword, keyPassword, keyAlias string) error {
	if !validJKSStructure(data) {
		return validation.Errorf("keystore", "keystore structure is invalid or truncated")
	}
	ks := keystore.New()
	if err := ks.Load(bytes.NewReader(data), []byte(keystorePassword)); err != nil {
		return validation.Errorf("keystorePassword", "keystore password is incorrect or the keystore is corrupted")
	}
	entry, err := ks.GetPrivateKeyEntry(keyAlias, []byte(keyPassword))
	switch {
	case err == nil:
		return validateSigningEntry(entry)
	case errors.Is(err, keystore.ErrEntryNotFound):
		return validation.Errorf("keyAlias", "alias %q not found in the keystore (available: %s)", keyAlias, strings.Join(ks.Aliases(), ", "))
	case errors.Is(err, keystore.ErrWrongEntryType):
		return validation.Errorf("keyAlias", "alias %q is not a private key entry", keyAlias)
	default:
		return validation.Errorf("keyPassword", "key password is incorrect for alias %q", keyAlias)
	}
}

func validateSigningEntry(entry keystore.PrivateKeyEntry) error {
	key, err := x509.ParsePKCS8PrivateKey(entry.PrivateKey)
	if err != nil {
		return validation.Errorf("keystore", "entry does not contain a valid PKCS8 private key")
	}
	signer, ok := key.(crypto.Signer)
	if !ok || len(entry.CertificateChain) == 0 {
		return validation.Errorf("keystore", "entry must contain a signing key and its certificate")
	}
	for i, certificate := range entry.CertificateChain {
		cert, err := x509.ParseCertificate(certificate.Content)
		if err != nil {
			return validation.Errorf("keystore", "entry contains an invalid certificate")
		}
		if i == 0 {
			publicKey, err := x509.MarshalPKIXPublicKey(signer.Public())
			if err != nil || !bytes.Equal(publicKey, cert.RawSubjectPublicKeyInfo) {
				return validation.Errorf("keystore", "signing certificate does not match the private key")
			}
		}
	}
	return nil
}

func validatePKCS12(data []byte, keystorePassword, keyPassword, keyAlias string) error {
	if keyPassword != keystorePassword {
		return validation.Errorf("keyPassword", "PKCS12 keystores with a key password different from the keystore password are not supported; re-export the keystore with a single password")
	}
	blocks, err := pkcs12.ToPEM(data, keystorePassword)
	if err != nil {
		switch {
		case errors.Is(err, pkcs12.ErrIncorrectPassword):
			return validation.Errorf("keystorePassword", "keystore password is incorrect")
		case errors.Is(err, pkcs12.ErrDecryption):
			return validation.Errorf("keyPassword", "key password is incorrect")
		default:
			return validation.Errorf("keystore", "file is not a valid PKCS12 keystore")
		}
	}
	var aliases []string
	unnamedKeys := 0
	for _, block := range blocks {
		if block.Type != "PRIVATE KEY" {
			continue
		}
		alias, ok := block.Headers["friendlyName"]
		if !ok {
			unnamedKeys++
			continue
		}
		if strings.EqualFold(alias, keyAlias) {
			return nil
		}
		aliases = append(aliases, alias)
	}
	switch {
	case len(aliases) == 0 && unnamedKeys == 0:
		return validation.Errorf("keystore", "keystore contains no private key")
	case unnamedKeys > 0:
		return validation.Errorf("keyAlias", "keystore contains a private key without an alias; re-export it with an explicit alias (for example, openssl pkcs12 -export -name upload)")
	default:
		return validation.Errorf("keyAlias", "alias %q not found in the keystore (available: %s)", keyAlias, strings.Join(aliases, ", "))
	}
}
