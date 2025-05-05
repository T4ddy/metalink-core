package util

import (
	"crypto/rand"
	"encoding/hex"
)

// GenerateUniqueID generates a random unique ID with specified length in bytes
func GenerateUniqueID(lengthInBytes int) (string, error) {
	bytes := make([]byte, lengthInBytes)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(bytes), nil
}
