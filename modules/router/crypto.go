package router

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// ---- Encryption key loading ---------------------------------------------
//
// ROUTER_SECRET_KEY must be a base64-encoded 32-byte (256-bit) key, e.g.
// generated once via: openssl rand -base64 32
//
// This key should live in a secrets manager (Vault / AWS Secrets Manager /
// GCP Secret Manager) in production, injected into the environment - never
// committed, never derived from anything guessable, and rotated
// independently of any individual router's password. Losing this key means
// every stored router password becomes permanently unrecoverable (by
// design - there is no backdoor decryption path).

var (
	cryptoKeyOnce sync.Once
	cryptoKey     []byte
	cryptoKeyErr  error
)

func loadCryptoKey() ([]byte, error) {
	cryptoKeyOnce.Do(func() {
		raw := os.Getenv("ROUTER_SECRET_KEY")
		if raw == "" {
			cryptoKeyErr = errors.New("router: ROUTER_SECRET_KEY is not set")
			return
		}

		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			cryptoKeyErr = fmt.Errorf("router: ROUTER_SECRET_KEY is not valid base64: %w", err)
			return
		}
		if len(key) != 32 {
			cryptoKeyErr = fmt.Errorf("router: ROUTER_SECRET_KEY must decode to 32 bytes (AES-256), got %d", len(key))
			return
		}
		cryptoKey = key
	})
	return cryptoKey, cryptoKeyErr
}

// EncryptSecret encrypts plaintext (e.g. a router API password) using
// AES-256-GCM. Output is base64( nonce || ciphertext ), safe to store
// directly in a single DB column. Each call generates a fresh random nonce,
// so encrypting the same plaintext twice produces different ciphertext -
// this also means ciphertext alone can't be used to detect password reuse
// across routers.
func EncryptSecret(plaintext string) (string, error) {
	key, err := loadCryptoKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("router: failed to init cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("router: failed to init gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("router: failed to generate nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptSecret reverses EncryptSecret. Returns an error (rather than
// garbage plaintext) if the ciphertext was tampered with, truncated, or
// encrypted under a different key - GCM authenticates on decrypt, so this
// also detects DB row corruption/tampering, not just wrong keys.
func DecryptSecret(encoded string) (string, error) {
	if encoded == "" {
		return "", errors.New("router: cannot decrypt empty secret")
	}

	key, err := loadCryptoKey()
	if err != nil {
		return "", err
	}

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("router: stored secret is not valid base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("router: failed to init cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("router: failed to init gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("router: stored secret is shorter than the nonce size")
	}

	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("router: failed to decrypt secret (wrong key or tampered data): %w", err)
	}

	return string(plaintext), nil
}
