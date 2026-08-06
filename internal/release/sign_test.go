package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

func TestParsePrivateKeyFormatsAndFailures(t *testing.T) {
	t.Parallel()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: encoded,
	})
	tests := []struct {
		name    string
		input   []byte
		wantErr bool
	}{
		{name: "PKCS8 PEM", input: pemKey},
		{
			name:  "base64 seed",
			input: []byte(base64.StdEncoding.EncodeToString(seed)),
		},
		{
			name: "base64 private key",
			input: []byte(
				base64.StdEncoding.EncodeToString(privateKey),
			),
		},
		{
			name: "mismatched private public half",
			input: func() []byte {
				corrupted := append(ed25519.PrivateKey(nil), privateKey...)
				corrupted[len(corrupted)-1] ^= 0xff
				return []byte(base64.StdEncoding.EncodeToString(corrupted))
			}(),
			wantErr: true,
		},
		{name: "invalid base64", input: []byte("%%%"), wantErr: true},
		{
			name: "wrong decoded length",
			input: []byte(
				base64.StdEncoding.EncodeToString([]byte("short")),
			),
			wantErr: true,
		},
		{
			name: "malformed PEM",
			input: pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: []byte("invalid"),
			}),
			wantErr: true,
		},
		{
			name:    "trailing PEM data",
			input:   append(append([]byte(nil), pemKey...), []byte("unexpected")...),
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parsePrivateKey(test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("parsePrivateKey() error = %v", err)
			}
		})
	}
}

func TestParsePrivateKeyRejectsNonEd25519PEM(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsePrivateKey(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: encoded,
	})); err == nil {
		t.Fatal("RSA signing key was accepted")
	}
}
