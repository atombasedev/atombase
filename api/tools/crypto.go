package tools

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
)

var (
	ErrInvalidKey        = errors.New("encryption key must be 32 bytes (64 hex chars)")
	ErrDecryptionFailed  = errors.New("decryption failed: invalid ciphertext")
	ErrEncryptionNotInit = errors.New("encryption not initialized")
)

var gcm cipher.AEAD

// InitEncryption initializes AES-GCM encryption with the given hex-encoded key.
// Key must be 32 bytes (256-bit), provided as 64 hex characters.
func InitEncryption(hexKey string) error {
	if hexKey == "" {
		return nil // Encryption disabled
	}

	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return ErrInvalidKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}

	gcm, err = cipher.NewGCM(block)
	if err != nil {
		return err
	}

	return nil
}

// Encrypt encrypts plaintext using AES-GCM.
// Returns nonce prepended to ciphertext.
func Encrypt(plaintext []byte) ([]byte, error) {
	if gcm == nil {
		return nil, ErrEncryptionNotInit
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext using AES-GCM.
// Expects nonce prepended to ciphertext.
func Decrypt(ciphertext []byte) ([]byte, error) {
	if gcm == nil {
		return nil, ErrEncryptionNotInit
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrDecryptionFailed
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// EncryptionEnabled returns true if encryption has been initialized.
func EncryptionEnabled() bool {
	return gcm != nil
}
