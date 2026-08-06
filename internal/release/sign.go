package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
)

func parsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	if block, rest := pem.Decode(data); block != nil {
		defer clear(block.Bytes)
		if len(bytes.TrimSpace(rest)) != 0 {
			return nil, errors.New("parse release signing key: trailing PEM data")
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 release signing key: %w", err)
		}
		key, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf(
				"parse release signing key: require Ed25519, got %T",
				parsed,
			)
		}
		return validatedPrivateKey(key)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf(
			"parse release signing key: require PKCS#8 PEM or base64: %w",
			err,
		)
	}
	defer clear(decoded)
	switch len(decoded) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(decoded), nil
	case ed25519.PrivateKeySize:
		return validatedPrivateKey(ed25519.PrivateKey(decoded))
	default:
		return nil, fmt.Errorf(
			"parse release signing key: decoded length %d, require %d-byte seed or %d-byte private key",
			len(decoded),
			ed25519.SeedSize,
			ed25519.PrivateKeySize,
		)
	}
}

func validatedPrivateKey(key ed25519.PrivateKey) (ed25519.PrivateKey, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf(
			"parse release signing key: private key length is %d, require %d",
			len(key),
			ed25519.PrivateKeySize,
		)
	}
	derived := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	defer clear(derived)
	if subtle.ConstantTimeCompare(key, derived) != 1 {
		return nil, errors.New(
			"parse release signing key: public key does not match private seed",
		)
	}
	return append(ed25519.PrivateKey(nil), key...), nil
}

func signChecksums(
	checksums []byte,
	privateKeyData []byte,
) ([]byte, []byte, error) {
	privateKey, err := parsePrivateKey(privateKeyData)
	if err != nil {
		return nil, nil, err
	}
	defer clear(privateKey)
	signature := ed25519.Sign(privateKey, checksums)
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf(
			"encode release public key: unexpected key type",
		)
	}
	if !ed25519.Verify(publicKey, checksums, signature) {
		return nil, nil, errors.New("verify release checksum signature")
	}
	encodedPublic, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode release public key: %w", err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: encodedPublic,
	})
	return signature, publicPEM, nil
}
