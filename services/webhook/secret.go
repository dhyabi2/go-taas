package webhook

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// secretPrefix is the human-readable prefix of a generated signing
// secret, mirroring Stripe's whsec_ convention (AD3).
const secretPrefix = "whsec_"

// GenerateSecret returns a random 32-byte signing secret, base64url-
// encoded with a whsec_ prefix (AD3).
func GenerateSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("webhook: generate secret: %w", err)
	}
	return secretPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// deriveKey derives a 32-byte AES-256 key from the master key material.
// A 32-byte master key is used directly; any other length is hashed with
// SHA-256 so a passphrase of any length works (AD3).
func deriveKey(masterKey string) []byte {
	if len(masterKey) == 32 {
		return []byte(masterKey)
	}
	sum := sha256.Sum256([]byte(masterKey))
	return sum[:]
}

// EncryptSecret encrypts a plaintext secret with AES-256-GCM using the
// master key (AD3). The ciphertext is nonce || ciphertext.
func EncryptSecret(plaintext, masterKey string) ([]byte, error) {
	block, err := aes.NewCipher(deriveKey(masterKey))
	if err != nil {
		return nil, fmt.Errorf("webhook: encrypt secret: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("webhook: encrypt secret: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("webhook: encrypt secret: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// DecryptSecret decrypts an AES-256-GCM ciphertext (nonce || ciphertext)
// back to the plaintext secret (AD3).
func DecryptSecret(ciphertext []byte, masterKey string) (string, error) {
	block, err := aes.NewCipher(deriveKey(masterKey))
	if err != nil {
		return "", fmt.Errorf("webhook: decrypt secret: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("webhook: decrypt secret: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", errors.New("webhook: decrypt secret: ciphertext too short")
	}
	nonce, ct := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("webhook: decrypt secret: %w", err)
	}
	return string(plaintext), nil
}
