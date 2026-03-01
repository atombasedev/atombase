package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

func shaHash(secret string) []byte {
	hash := sha256.Sum256([]byte(secret))
	return hash[:]
}

func ID128() string {
	return B64(16)
}

func ID256() string {
	return B64(32)
}

func B64(bytes int) string {
	b := make([]byte, bytes)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
