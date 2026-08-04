// Package secrets provides credential encryption and log redaction. The
// master key comes from the environment (KMS-backed keyrings arrive in
// Phase 7 behind the same Keyring seam). Decrypted values are registered
// with the global redactor so they can never appear in logs.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Keyring encrypts and decrypts credential payloads.
type Keyring interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// AESKeyring is an AES-256-GCM Keyring with a static master key.
type AESKeyring struct {
	aead cipher.AEAD
}

// NewAESKeyring builds a keyring from a 32-byte hex-encoded master key
// (e.g. the CORE_MASTER_KEY env var).
func NewAESKeyring(hexKey string) (*AESKeyring, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("secrets: master key is not hex: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("secrets: master key must be 32 bytes (64 hex chars)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: gcm: %w", err)
	}
	return &AESKeyring{aead: aead}, nil
}

// GenerateMasterKey returns a fresh random hex master key (for dev/harness).
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("secrets: generate key: %w", err)
	}
	return hex.EncodeToString(key), nil
}

// Encrypt implements Keyring. Output is nonce || ciphertext.
func (k *AESKeyring) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secrets: nonce: %w", err)
	}
	return k.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt implements Keyring.
func (k *AESKeyring) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < k.aead.NonceSize() {
		return nil, errors.New("secrets: ciphertext too short")
	}
	nonce, sealed := ciphertext[:k.aead.NonceSize()], ciphertext[k.aead.NonceSize():]
	plaintext, err := k.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, errors.New("secrets: decrypt failed")
	}
	return plaintext, nil
}
