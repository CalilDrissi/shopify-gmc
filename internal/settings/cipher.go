package settings

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

const KeyEnvVar = "SETTINGS_ENCRYPTION_KEY"

var (
	ErrEncryptionKeyMissing = errors.New("settings: " + KeyEnvVar + " is not set")
	ErrInvalidCiphertext    = errors.New("settings: invalid ciphertext")
)

// Cipher provides authenticated AES-256-GCM encryption with a per-write nonce
// prepended to the ciphertext.
type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(b64Key string) (*Cipher, error) {
	if b64Key == "" {
		return nil, ErrEncryptionKeyMissing
	}
	key, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil {
		return nil, fmt.Errorf("settings: decode key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("settings: key must decode to 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("settings: aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("settings: gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// NewCipherFromEnv reads SETTINGS_ENCRYPTION_KEY and returns a Cipher.
// Returns ErrEncryptionKeyMissing when unset — boot code is responsible for
// refusing to start in production when that error is returned.
func NewCipherFromEnv() (*Cipher, error) {
	return NewCipher(os.Getenv(KeyEnvVar))
}

// MustHaveKey returns nil if the env var is set, ErrEncryptionKeyMissing
// otherwise. Intended for the prod boot guard before constructing the service.
func MustHaveKey() error {
	if os.Getenv(KeyEnvVar) == "" {
		return ErrEncryptionKeyMissing
	}
	return nil
}

func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("settings: read nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(blob) < n+c.aead.Overhead() {
		return nil, ErrInvalidCiphertext
	}
	nonce, ct := blob[:n], blob[n:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	return pt, nil
}
