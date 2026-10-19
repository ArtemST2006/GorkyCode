package service

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// sessionTokenBytes is the amount of randomness in a session token.
const sessionTokenBytes = 32

// newSessionToken returns a random opaque session token.
func newSessionToken() (string, error) {
	token := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}
