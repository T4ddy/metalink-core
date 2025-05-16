package util

import (
	"encoding/base64"
	"errors"

	"github.com/google/uuid"
)

// ShortUUID generates a short UUID with 22 symbols
func ShortUUID() string {
	u := uuid.New()
	return base64.RawURLEncoding.EncodeToString(u[:]) // 22 symbols
}

// GenerateUUIDWithLength generates a UUID with a specified length
func GenerateUUIDWithLength(length int) (string, error) {
	u := uuid.New()
	encoded := base64.RawURLEncoding.EncodeToString(u[:]) // 22 symbols without padding

	if length > len(encoded) {
		return "", errors.New("requested length exceeds the maximum possible")
	}

	return encoded[:length], nil
}
