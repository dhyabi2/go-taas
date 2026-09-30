package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignProducesHeader(t *testing.T) {
	body := []byte(`{"id":"evt-1","type":"deployment.status_changed"}`)
	secret := "whsec_testsecret"
	ts := int64(1700000000)
	header := Sign(body, secret, ts)
	assert.Contains(t, header, "t=1700000000,")
	assert.Contains(t, header, "v1=")
	// The signature is deterministic for the same body/secret/ts.
	assert.Equal(t, header, Sign(body, secret, ts))
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"id":"evt-1"}`)
	secret := "whsec_testsecret"
	header := SignNow(body, secret)
	assert.True(t, VerifySignature(header, body, secret, 300))
	// Tampered body fails.
	assert.False(t, VerifySignature(header, []byte(`{"id":"evt-2"}`), secret, 300))
	// Wrong secret fails.
	assert.False(t, VerifySignature(header, body, "whsec_wrong", 300))
	// Malformed header fails.
	assert.False(t, VerifySignature("garbage", body, secret, 300))
}

func TestGenerateSecret(t *testing.T) {
	s1, err := GenerateSecret()
	require.NoError(t, err)
	s2, err := GenerateSecret()
	require.NoError(t, err)
	assert.NotEqual(t, s1, s2)
	assert.Contains(t, s1, secretPrefix)
	assert.Len(t, s1, len(secretPrefix)+43) // 32 bytes base64url
}

func TestEncryptDecryptSecret(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef" // 32 bytes
	plaintext := "whsec_mysecret"
	ct, err := EncryptSecret(plaintext, key)
	require.NoError(t, err)
	// Ciphertext is not the plaintext.
	assert.NotContains(t, string(ct), plaintext)
	got, err := DecryptSecret(ct, key)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
	// Wrong key fails.
	_, err = DecryptSecret(ct, "fedcba9876543210fedcba9876543210")
	assert.Error(t, err)
	// Short ciphertext fails.
	_, err = DecryptSecret([]byte("short"), key)
	assert.Error(t, err)
}

func TestEncryptSecretPassphraseDerivation(t *testing.T) {
	// A non-32-byte passphrase is hashed to a 32-byte key.
	key := "a-short-passphrase"
	plaintext := "whsec_secret"
	ct, err := EncryptSecret(plaintext, key)
	require.NoError(t, err)
	got, err := DecryptSecret(ct, key)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}
