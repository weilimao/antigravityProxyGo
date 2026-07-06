package settings

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const encryptedPrefix = "enc:v1:"

// deriveEncryptionKey derives a 256-bit AES key from machine-specific entropy.
// This is not meant to be unbreakable—it's defense-in-depth to prevent trivial
// plaintext credential theft from config.json.
func deriveEncryptionKey() []byte {
	var seeds []string

	// Use a combination of stable machine identifiers as key material
	if hostname, err := os.Hostname(); err == nil {
		seeds = append(seeds, hostname)
	}
	if home, err := os.UserHomeDir(); err == nil {
		seeds = append(seeds, home)
	}
	seeds = append(seeds, runtime.GOOS, runtime.GOARCH)

	combined := strings.Join(seeds, "|")
	hash := sha256.Sum256([]byte("antigravity-proxy-credential-key:" + combined))
	return hash[:]
}

// EncryptCredential encrypts a plaintext credential string using AES-256-GCM.
// Returns a prefixed base64-encoded ciphertext string.
func EncryptCredential(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	key := deriveEncryptionKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	encoded := base64.StdEncoding.EncodeToString(ciphertext)
	return encryptedPrefix + encoded, nil
}

// DecryptCredential decrypts a credential string that was encrypted by EncryptCredential.
// If the string is not prefixed with the encryption marker, it is returned as-is (plaintext fallback
// for backward compatibility with existing config files).
func DecryptCredential(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}

	// Backward compatibility: if not encrypted, return as-is
	if !strings.HasPrefix(stored, encryptedPrefix) {
		return stored, nil
	}

	encoded := strings.TrimPrefix(stored, encryptedPrefix)
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode credential: %w", err)
	}

	key := deriveEncryptionKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertextBytes := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credential: %w", err)
	}

	return string(plaintext), nil
}

// IsEncrypted returns true if the stored value is already encrypted.
func IsEncrypted(stored string) bool {
	return strings.HasPrefix(stored, encryptedPrefix)
}
